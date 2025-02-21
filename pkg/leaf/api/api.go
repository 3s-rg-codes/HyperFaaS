package api

import (
	"context"
	"log"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerPB "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

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
		// There is no idle instance available
		instanceID, err = startInstance(ctx, workerID, state.FunctionID(req.FunctionId))
		if err != nil {
			return nil, err
		}
		log.Printf("Started new instance for function %s on worker %s, instanceID: %s", req.FunctionId, workerID, instanceID)
		s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateNew)
	} else {
		// An Idle instance was found
		s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateRunning)
	}

	resp, err := callWorker(ctx, workerID, state.FunctionID(req.FunctionId), instanceID, req)
	if err != nil {
		switch err.(type) {
		case *controller.ContainerCrashError, *controller.InstanceNotFoundError:
			s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateDown)
			// TODO: handle this and dont return.
			return nil, err
		case *WorkerDownError:
			s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateDown)
			s.scheduler.UpdateWorkerState(workerID, state.WorkerStateDown)
			return nil, err
		default:
			return nil, err
		}
	}
	log.Printf("Recieved response from worker %s, instanceID: %s, response: %v", workerID, instanceID, resp)
	// The instance is no longer running
	s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateIdle)

	return resp, nil
}

func NewLeafServer(scheduler scheduling.Scheduler) *LeafServer {
	return &LeafServer{scheduler: scheduler}
}

// TODO: refactor this to use a pool of connections.
// https://promisefemi.vercel.app/blog/grpc-client-connection-pooling
// https://github.com/processout/grpc-go-pool/blob/master/pool.go
func callWorker(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {
	conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := controllerPB.NewControllerClient(conn)

	callReq := &common.CallRequest{
		InstanceId: &common.InstanceID{Id: string(instanceID)},
		FunctionId: &common.FunctionID{Id: string(functionID)},
		Data:       req.Data,
	}

	resp, err := client.Call(ctx, callReq)
	if err != nil {
		// Check if it's a connection error
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unavailable {
			return nil, &WorkerDownError{WorkerID: workerID, err: err}
		}
		return nil, err
	}

	return &leaf.ScheduleCallResponse{Data: resp.Data, Error: resp.Error}, nil
}

func startInstance(ctx context.Context, workerID state.WorkerID, functionId state.FunctionID) (state.InstanceID, error) {
	conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	client := controllerPB.NewControllerClient(conn)

	// Todo : we need to agree on either functionId or imageTag
	startReq := &controllerPB.StartRequest{
		ImageTag: &controllerPB.ImageTag{Tag: string(functionId)},
	}

	instanceID, err := client.Start(ctx, startReq)
	if err != nil {
		return "", err
	}

	return state.InstanceID(instanceID.Id), nil
}
