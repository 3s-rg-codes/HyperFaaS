package main

import (
	"context"
	"time"

	functionRuntimeInterface "github.com/3s-rg-codes/HyperFaaS/pkg/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) (*functionRuntimeInterface.Response, error) {

	//sleep for 20 seconds
	time.Sleep(20 * time.Second)

	resp := &functionRuntimeInterface.Response{
		Data: "Finished Sleeping",
		Id:   in.Id,
	}

	return resp, nil
}
