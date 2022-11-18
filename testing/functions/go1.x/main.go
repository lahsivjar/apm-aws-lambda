package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"

	_ "go.elastic.co/apm/module/apmlambda/v2"
)

var coldstart = true

func Handle(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	lc, _ := lambdacontext.FromContext(ctx)
	log.Println("Example function log", lc.AwsRequestID)
	if (1+rand.Intn(5))%5 == 0 {
		time.Sleep(20 * time.Second)
	}
	response := events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       fmt.Sprintf("Hello from go!%s", lc.AwsRequestID),
		Headers: map[string]string{
			"coldstart": strconv.FormatBool(coldstart),
		},
	}
	coldstart = false
	return response, nil
}

func main() {
	lambda.Start(Handle)
}
