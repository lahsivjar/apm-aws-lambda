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

package app

import "github.com/aws/aws-sdk-go-v2/aws"

type appConfig struct {
	awsLambdaRuntimeAPI string
	awsConfig           aws.Config
	extensionName       string
	disableLogsAPI      bool
	logLevel            string
	logsapiAddr         string
}

type configOption func(*appConfig)

// WithLambdaRuntimeAPI sets the AWS Lambda Runtime API
// endpoint (normally taken from $AWS_LAMBDA_RUNTIME_API),
// used by the AWS client.
func WithLambdaRuntimeAPI(api string) configOption {
	return func(c *appConfig) {
		c.awsLambdaRuntimeAPI = api
	}
}

// WithExtensionName sets the extension name.
func WithExtensionName(name string) configOption {
	return func(c *appConfig) {
		c.extensionName = name
	}
}

// WithoutLogsAPI disables the logs api.
func WithoutLogsAPI() configOption {
	return func(c *appConfig) {
		c.disableLogsAPI = true
	}
}

// WithLogLevel sets the log level.
func WithLogLevel(level string) configOption {
	return func(c *appConfig) {
		c.logLevel = level
	}
}

// WithLogsapiAddress sets the listener address of the
// server listening for logs event.
func WithLogsapiAddress(s string) configOption {
	return func(c *appConfig) {
		c.logsapiAddr = s
	}
}

// WithAWSConfig sets the AWS config.
func WithAWSConfig(awsConfig aws.Config) configOption {
	return func(c *appConfig) {
		c.awsConfig = awsConfig
	}
}
