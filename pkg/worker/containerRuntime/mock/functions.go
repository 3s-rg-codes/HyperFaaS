package mock

import (
	"context"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
)

// Implementations of functions that can be used in the mock runtime

type fakeHelloFunction struct{}

func (f *fakeHelloFunction) HandleCall(ctx context.Context, in *commonpb.CallRequest) (*commonpb.CallResponse, error) {
	return &commonpb.CallResponse{
		Data:  []byte("HELLO WORLD!"),
		Error: nil,
	}, nil
}

type fakeEchoFunction struct{}

func (f *fakeEchoFunction) HandleCall(ctx context.Context, in *commonpb.CallRequest) (*commonpb.CallResponse, error) {
	return &commonpb.CallResponse{
		Data:  in.Data,
		Error: nil,
	}, nil
}

type fakeSimulFunction struct{}

func (f *fakeSimulFunction) HandleCall(ctx context.Context, in *commonpb.CallRequest) (*commonpb.CallResponse, error) {
	return &commonpb.CallResponse{
		Data:  []byte(""),
		Error: nil,
	}, nil
}
