package main

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

// this function crashes the container on purpose
func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {

	resp := &common.CallResponse{
		Data:  []byte(""),
		Error: nil,
	}

	//sleep for 2 seconds
	time.Sleep(2 * time.Second)
	//crash the container
	panic("crash")

	return resp, nil
}
