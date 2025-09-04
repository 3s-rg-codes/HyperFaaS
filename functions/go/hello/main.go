package main

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

func main() {
	f := functionRuntimeInterface.New(10)

	f.Ready(handler)
}

func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {
	resp := &common.CallResponse{
		Data:  []byte("HELLO WORLD!"),
		Error: nil,
	}

	return resp, nil
}
