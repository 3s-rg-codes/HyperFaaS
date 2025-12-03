package main

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/sleep/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"google.golang.org/grpc"
)

type sleepServer struct {
	pb.UnimplementedSleepServer
}

func (s *sleepServer) Sleep(ctx context.Context, req *pb.SleepRequest) (*pb.SleepReply, error) {
	duration := time.Duration(req.DurationNanos)
	time.Sleep(duration)

	return &pb.SleepReply{Message: "Finished Sleeping"}, nil
}

func main() {
	fn := functionRuntimeInterface.NewV2(30)

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterSleepServer(reg, &sleepServer{})
	})
}
