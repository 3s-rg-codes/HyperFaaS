package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(10)

	f.Ready(handler)
}

func handler(ctx context.Context, in *functionRuntimeInterface.Request) *functionRuntimeInterface.Response {
	// Simulate workload between 100ms and 200ms
	time.Sleep(time.Duration(rand.Intn(100)+100) * time.Millisecond)
	resp := &functionRuntimeInterface.Response{
		Data: []byte(""),
		Id:   in.Id,
	}

	return resp
}
