package api

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

// grpc api endpoints that leafLeader will expose

// these will be called by Leaders above in the leader chain

// The main "state" of the leader is all of the worker IPs.
// Would only be changed if a worker is added or removed.

type LeafServer struct {
	leaf.UnimplementedLeafServer
	scheduler scheduling.Scheduler
}

func (s *LeafServer) ScheduleCall(ctx context.Context, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {

	workerID, instanceID, err := s.scheduler.Schedule(ctx, state.FunctionID(req.FunctionId))
	if err != nil {
		return nil, err
	}

	if instanceID == "" {
		instanceID, err = startInstance(ctx, workerID, state.FunctionID(req.FunctionId))
		if err != nil {
			return nil, err
		}
	}

	// Update State to reflect that we are sending a call to this worker
	// Issue: what if the worker dies / container crashes before the call is processed? This update to the state would be erroneous.
	// We would need some logic that reconciles the state in case of failures.
	go s.scheduler.UpdateState(ctx, workerID, state.FunctionID(req.FunctionId), instanceID)

	resp, err := callWorker(ctx, workerID, instanceID, req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func NewLeafServer(scheduler scheduling.Scheduler) *LeafServer {
	return &LeafServer{scheduler: scheduler}
}

// TODO: refactor this to use a pool of connections.
// https://promisefemi.vercel.app/blog/grpc-client-connection-pooling
// https://github.com/processout/grpc-go-pool/blob/master/pool.go
func callWorker(ctx context.Context, workerID state.WorkerID, instanceID state.InstanceID, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {
	conn, err := grpc.NewClient(string(workerID))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := controller.NewControllerClient(conn)

	callReq := &common.CallRequest{
		InstanceId: &common.InstanceID{Id: string(instanceID)},
		Data:       req.Data,
	}

	resp, err := client.Call(ctx, callReq)
	if err != nil {
		return nil, err
	}

	return &leaf.ScheduleCallResponse{Data: resp.Data, Error: resp.Error}, nil
}

func startInstance(ctx context.Context, workerID state.WorkerID, functionId state.FunctionID) (state.InstanceID, error) {
	conn, err := grpc.NewClient(string(workerID))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	client := controller.NewControllerClient(conn)

	// Todo : we need to agree on either functionId or imageTag
	startReq := &controller.StartRequest{
		ImageTag: &controller.ImageTag{Tag: string(functionId)},
	}

	instanceID, err := client.Start(ctx, startReq)
	if err != nil {
		return "", err
	}

	return state.InstanceID(instanceID.Id), nil
}
