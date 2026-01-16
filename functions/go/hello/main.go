package main

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/hello/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"google.golang.org/grpc"
)

type helloServer struct {
	pb.UnimplementedHelloServer
}

func (s *helloServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{Message: "Hello, World!"}, nil
}

func main() {
	fn := functionRuntimeInterface.NewV2()

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterHelloServer(reg, &helloServer{})
	})
}
