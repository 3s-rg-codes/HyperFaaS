package main

import (
	"context"
	"time"

	functionRuntimeInterface "github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) (*functionRuntimeInterface.Response, error) {

	//sleep for 20 seconds
	time.Sleep(20 * time.Second)

	resp := &functionRuntimeInterface.Response{
		Data: []byte("Finished Sleeping"),
		Id:   in.Id,
	}

	return resp, nil
}
