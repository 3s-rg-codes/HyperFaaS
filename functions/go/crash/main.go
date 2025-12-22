package main

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/crash/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"google.golang.org/grpc"
)

type crashServer struct {
	pb.UnimplementedCrashServer
}

func (s *crashServer) Crash(ctx context.Context, req *pb.CrashRequest) (*pb.CrashReply, error) {
	// sleep for 2 seconds
	time.Sleep(2 * time.Second)
	// crash the container
	panic("crash")
}

func main() {
	fn := functionRuntimeInterface.NewV2(30)

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterCrashServer(reg, &crashServer{})
	})
}
