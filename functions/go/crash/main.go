package main

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
)

func main() {

	f := functionRuntimeInterface.New(120)

	f.Ready(handler)
}

// this function crashes the container on purpose
func handler(ctx context.Context, in *functionRuntimeInterface.Request) *functionRuntimeInterface.Response {

	resp := &functionRuntimeInterface.Response{
		Data:  []byte(""),
		Error: "Container crashed",
		Id:    in.Id,
	}

	//sleep for 2 seconds
	time.Sleep(2 * time.Second)
	//crash the container
	panic("crash")

	return resp
}
