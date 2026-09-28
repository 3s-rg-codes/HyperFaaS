package main

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc"
	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/functionruntime"
)

var isCold int32 = 1

type echoServer struct {
	echopb.UnimplementedEchoServer
}

func (s *echoServer) Echo(_ context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	cold := atomic.CompareAndSwapInt32(&isCold, 1, 0)
	return &echopb.EchoResponse{Data: req.GetData(), Cold: cold}, nil
}

func main() {
	fn := functionruntime.NewGRPC()
	fn.Ready(func(reg grpc.ServiceRegistrar) {
		echopb.RegisterEchoServer(reg, &echoServer{})
	})
}
