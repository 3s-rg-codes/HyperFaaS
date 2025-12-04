package main

import (
	"context"
	"fmt"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/grpc-proxy-greeter/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"google.golang.org/grpc"
)

type greeterServer struct {
	pb.UnimplementedGreeterServer
}

func (s *greeterServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	name := req.GetName()
	if name == "" {
		name = "world"
	}
	return &pb.HelloReply{Message: fmt.Sprintf("Hello, %s!", name)}, nil
}

func main() {
	fn := functionRuntimeInterface.NewV2(30)

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterGreeterServer(reg, &greeterServer{})
	})
}
