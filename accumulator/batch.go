// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package accumulator

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

var (
	// ErrMetadataUnavailable is returned when a lambda data is added to
	// the batch without metadata being set.
	ErrMetadataUnavailable = errors.New("metadata is not yet available")
	// ErrBatchFull signfies that the batch has reached full capacity
	// and cannot accept more entries.
	ErrBatchFull = errors.New("batch is full")
	// ErrInvalidEncoding is returned for any APMData that is encoded
	// with any encoding format
	ErrInvalidEncoding = errors.New("encoded data not supported")
)

var (
	maxSizeThreshold = 0.9
	zeroTime         = time.Time{}
)

// Batch manages the data that needs to be shipped to APM Server. It holds
// all the invocations that have not yet been shipped to the APM Server and
// is responsible for correlating the invocation with the APM data collected
// from all sources (logs API & APM Agents). As the batch gets the required
// data it marks the data ready for shipping to APM Server.
type Batch struct {
	// metadataBytes is the size of the metadata in bytes
	metadataBytes int
	// buf holds data that is ready to be shipped to APM-Server
	buf bytes.Buffer
	// invocations holds the data for a specific invocation with
	// request ID as the key.
	invocations                 map[string]*Invocation
	count                       int
	age                         time.Time
	maxSize                     int
	maxAge                      time.Duration
	currentlyExecutingRequestID string

	// TODO: @lahsivjar remove requirements of a mutex; currently it is
	// required because the invocations need to be accessed from logsapi
	// as the processed log output of logsapi doesn't have the necessary
	// information.
	mu sync.RWMutex
}

// NewBatch creates a new BatchData which can accept a
// maximum number of entries as specified by the arguments.
func NewBatch(maxSize int, maxAge time.Duration) *Batch {
	return &Batch{
		invocations: make(map[string]*Invocation),
		maxSize:     maxSize,
		maxAge:      maxAge,
	}
}

// RegisterInvocation registers a new function invocation against its request
// ID. It also updates the caches for currently executing request ID.
func (b *Batch) RegisterInvocation(
	requestID, functionARN string,
	deadlineMs int64,
	timestamp time.Time,
) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.invocations[requestID] = &Invocation{
		RequestID:   requestID,
		FunctionARN: functionARN,
		DeadlineMs:  deadlineMs,
		Timestamp:   timestamp,
	}
	b.currentlyExecutingRequestID = requestID
}

// OnAgentInit caches the transactionID and the payload for the currently
// executing invocation as reported by the agent. The traceID and transactionID
// will be used to create a new transaction in an event the actual transaction
// is not reported by the agent due to unexpected termination.
func (b *Batch) OnAgentInit(transactionID string, traceID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	i, ok := b.invocations[b.currentlyExecutingRequestID]
	if !ok {
		return fmt.Errorf("invocation for requestID %s does not exist", b.currentlyExecutingRequestID)
	}
	i.TransactionID, i.TraceID = transactionID, traceID
	return nil
}

// AddAgentData adds a data received from agent. For a specific invocation
// agent data is always received in the same invocation.
func (b *Batch) AddAgentData(apmData APMData) error {
	raw, err := GetUncompressedBytes(apmData.Data, apmData.ContentEncoding)
	if err != nil {
		return err
	}
	// A request body can either be empty or have a ndjson content with
	// first line being metadata.
	data := bytes.Split(raw, []byte("\n"))
	if len(data) == 0 {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	inc, ok := b.invocations[b.currentlyExecutingRequestID]
	if !ok {
		return fmt.Errorf("invocation for requestID %s does not exist", b.currentlyExecutingRequestID)
	}
	// Set metadata if not already set
	if b.metadataBytes == 0 {
		b.metadataBytes, _ = b.buf.Write(data[0])
	}
	for i := 1; i < len(data); i++ {
		if inc.NeedProxyTransaction() {
			switch t := findEventType(data[i]); string(t) {
			case "transaction":
				res := gjson.GetBytes(data[i], "transaction.id")
				if res.Str != "" && inc.TransactionID == res.Str {
					inc.TransactionObserved = true
					continue
				}
			}
		}
		if err := b.addData(data[i]); err != nil {
			return err
		}
	}
	return nil
}

// OnLambdaLogRuntimeDone prepares the data for the invocation to be shipped
// to APM Server. It accepts requestID and status of the invocation both of
// which can be retrieved after parsing `platform.runtimeDone` event.
func (b *Batch) OnLambdaLogRuntimeDone(reqID, status string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.finalizeInvocation(reqID, status)
}

// OnShutdown flushes the data for shipping to APM Server by finalizing all
// the invocation in the batch. If we haven't received a platform.runtimeDone
// event for an invocation so far we won't be able to recieve it in time thus
// the status needs to be guessed based on the available information.
func (b *Batch) OnShutdown(status string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, inc := range b.invocations {
		if err := b.finalizeInvocation(inc.RequestID, status); err != nil {
			return err
		}
	}
	return nil
}

// AddLambdaData adds a new entry to the batch. Returns ErrBatchFull
// if batch has reached its maximum size.
func (b *Batch) AddLambdaData(d []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.addData(d)
}

// Count return the number of APMData entries in batch.
func (b *Batch) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// ShouldShip indicates when a batch is ready for sending.
// A batch is marked as ready for flush when one of the
// below conditions is reached:
// 1. max size is greater than threshold (90% of maxSize)
// 2. batch is older than maturity age
func (b *Batch) ShouldShip() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return (b.count >= int(float64(b.maxSize)*maxSizeThreshold)) ||
		(!b.age.IsZero() && time.Since(b.age) > b.maxAge)
}

// Reset resets the batch to prepare for new set of data
func (b *Batch) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count, b.age = 0, zeroTime
	b.buf.Truncate(b.metadataBytes)
}

// ToAPMData returns APMData with metadata and the accumulated batch
func (b *Batch) ToAPMData() APMData {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return APMData{
		Data: b.buf.Bytes(),
	}
}

func (b *Batch) finalizeInvocation(reqID, status string) error {
	inc, ok := b.invocations[reqID]
	if !ok {
		return fmt.Errorf("invocation for requestID %s does not exist", reqID)
	}
	defer delete(b.invocations, reqID)
	if proxyTxn := inc.Finalize(status); proxyTxn != nil {
		return b.addData(proxyTxn)
	}
	return nil
}

func (b *Batch) addData(data []byte) error {
	if b.metadataBytes == 0 {
		return ErrMetadataUnavailable
	}
	if b.count == b.maxSize {
		return ErrBatchFull
	}
	if err := b.buf.WriteByte('\n'); err != nil {
		return err
	}
	if _, err := b.buf.Write(data); err != nil {
		return err
	}
	if b.count == 0 {
		// For first entry, set the age of the batch
		b.age = time.Now()
	}
	b.count++
	return nil
}

func findEventType(body []byte) []byte {
	var quote byte
	var key []byte
	for i, r := range body {
		if r == '"' || r == '\'' {
			quote = r
			key = body[i+1:]
			break
		}
	}
	end := bytes.IndexByte(key, quote)
	if end == -1 {
		return nil
	}
	return key[:end]
}
