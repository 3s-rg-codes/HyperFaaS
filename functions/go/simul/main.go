package main

import (
	"context"
	"math/rand"
	"time"

	functionRuntimeInterface "github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) (*functionRuntimeInterface.Response, error) {
	// Simulate workload between 100ms ans 2 seconds
	time.Sleep(time.Duration(rand.Intn(1900)+100) * time.Millisecond)
	resp := &functionRuntimeInterface.Response{
		Data: []byte(""),
		Id:   in.Id,
	}

	return resp, nil
}
