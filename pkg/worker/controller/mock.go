package controller

import (
	"context"
	"math/rand"
	"time"

	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ workerpb.WorkerServer = MockController{}

type MockController struct {
	workerpb.UnimplementedWorkerServer
}

// Metrics implements worker.WorkerServer.
// Subtle: this method shadows the method (UnimplementedWorkerServer).Metrics of MockController.UnimplementedWorkerServer.
func (m MockController) Metrics(context.Context, *workerpb.MetricsRequest) (*workerpb.MetricsUpdate, error) {
	panic("unimplemented")
}

// SignalReady implements worker.WorkerServer.
// Subtle: this method shadows the method (UnimplementedWorkerServer).SignalReady of MockController.UnimplementedWorkerServer.
func (m MockController) SignalReady(context.Context, *workerpb.SignalReadyRequest) (*emptypb.Empty, error) {
	panic("unimplemented")
}

// Status implements worker.WorkerServer.
// Subtle: this method shadows the method (UnimplementedWorkerServer).Status of MockController.UnimplementedWorkerServer.
func (m MockController) Status(_ *workerpb.StatusRequest, stream grpc.ServerStreamingServer[workerpb.StatusUpdate]) error {
	// Keep the stream open but do not send any messages.
	// Exit only when the client closes the stream or context is cancelled.
	<-stream.Context().Done()
	return stream.Context().Err()
}

// mustEmbedUnimplementedWorkerServer implements worker.WorkerServer.
// Subtle: this method shadows the method (UnimplementedWorkerServer).mustEmbedUnimplementedWorkerServer of MockController.UnimplementedWorkerServer.
func (m MockController) mustEmbedUnimplementedWorkerServer() {
	panic("unimplemented")
}

func (m MockController) Start(ctx context.Context, req *workerpb.StartRequest) (*workerpb.StartResponse, error) {
	time.Sleep(time.Duration(rand.Intn(250)) * time.Millisecond)
	return &workerpb.StartResponse{
		InstanceId:         "instance-id",
		InstanceInternalIp: "127.0.0.1:56789",
		InstanceExternalIp: "127.0.0.1:56789",
		InstanceName:       "mock-instance",
	}, nil
}

func (m MockController) Stop(ctx context.Context, req *workerpb.StopRequest) (*workerpb.StopResponse, error) {
	return &workerpb.StopResponse{InstanceId: req.InstanceId}, nil
}
