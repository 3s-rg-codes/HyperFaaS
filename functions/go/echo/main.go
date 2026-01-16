package main

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/echo/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"google.golang.org/grpc"
)

type echoServer struct {
	pb.UnimplementedEchoServer
}

func (s *echoServer) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoReply, error) {
	return &pb.EchoReply{Data: req.Data}, nil
}

func main() {
	fn := functionRuntimeInterface.NewV2()

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterEchoServer(reg, &echoServer{})
	})
}
