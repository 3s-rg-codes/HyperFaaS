package functions

import (
	"context"
	"github.com/3s-rg-codes/HyperFaaS/pkg/functionRuntimeInterface/mock"
	"time"
)

func HandlerHello(ctx context.Context, in *mock.Request) (*mock.Response, error) {
	resp := &mock.Response{
		Data: "HELLO WORLD!",
		Id:   in.Id,
	}

	return resp, nil
}

func HandlerSleep(ctx context.Context, in *mock.Request) (*mock.Response, error) {
	//sleep for 20 seconds
	time.Sleep(20 * time.Second)

	resp := &mock.Response{
		Data: "Finished Sleeping",
		Id:   in.Id,
	}

	return resp, nil
}

func HandlerCrash(ctx context.Context, in *mock.Request) (*mock.Response, error) {
	resp := &mock.Response{
		Data: "",
		Id:   in.Id,
	}

	//sleep for 2 seconds
	time.Sleep(2 * time.Second)
	//crash the container
	panic("crash")

	return resp, nil
}

func HandlerEcho(ctx context.Context, in *mock.Request) (*mock.Response, error) {
	resp := &mock.Response{
		Data: in.Data,
		Id:   in.Id,
	}

	return resp, nil
}
