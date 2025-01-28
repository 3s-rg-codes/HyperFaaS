package main

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) *functionRuntimeInterface.Response {

	resp := &functionRuntimeInterface.Response{
		Data: in.Data,
		Id:   in.Id,
	}

	return resp
}
