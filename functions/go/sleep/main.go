package main

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

func main() {
	f := functionRuntimeInterface.New(10)

	f.Ready(handler)
}

func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {
	// sleep for 20 seconds
	time.Sleep(20 * time.Second)

	resp := &common.CallResponse{
		Data:  []byte("Finished Sleeping"),
		Error: nil,
	}

	return resp, nil
}
