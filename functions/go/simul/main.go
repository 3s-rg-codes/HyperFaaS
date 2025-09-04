package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

func main() {
	f := functionRuntimeInterface.New(10)

	f.Ready(handler)
}

func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {
	// Simulate workload between 100ms and 200ms
	time.Sleep(time.Duration(rand.Intn(100)+100) * time.Millisecond)
	resp := &common.CallResponse{
		Data:  []byte(""),
		Error: nil,
	}

	return resp, nil
}
