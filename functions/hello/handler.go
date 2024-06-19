package main

import (
	"context"

	functionRuntimeInterface "github.com/3s-rg-codes/HyperFaaS/pkg/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(10)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) (*functionRuntimeInterface.Response, error) {

	resp := &functionRuntimeInterface.Response{
		Data: "HELLO WORLD!",
		Id:   in.Id,
	}

	return resp, nil
}
