package mock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"github.com/google/uuid"
)

type MockRuntime struct {
	cr.ContainerRuntime
	logger    *slog.Logger
	callerRef *caller.CallerServer
	Running   map[string][]RunningInstance
	Dict      map[string]string
}

type RunningInstance struct {
	ctx context.Context
	id  string
}

func NewMockRuntime(server *caller.CallerServer, logger *slog.Logger) *MockRuntime {
	return &MockRuntime{
		logger:    logger,
		callerRef: server,
		Running:   make(map[string][]RunningInstance),
		Dict:      make(map[string]string),
	}
}

func (m *MockRuntime) Start(ctx context.Context, imageTag string, config *controller.Config) (string, error) {

	longID, err := uuid.NewUUID()
	if err != nil {
		return "", err
	}
	instanceID := longID.String()[:8]      //truncating the ID to 8 for compatibility
	controlContext := context.Background() //cant be the same context of this function since it'll be canceled when the function returns, we will use this to stop the goroutine when the function should be stopped
	instance := RunningInstance{
		ctx: controlContext,
		id:  instanceID,
	}
	m.Running[imageTag] = append(m.Running[imageTag], instance)
	payload := &pb.Payload{FirstExecution: true, InstanceId: instanceID}
	switch imageTag {
	case "hello":
		go fakeHelloFunction(payload, controlContext, m.callerRef, m.logger)
	case "echo":

		go fakeEchoFunction(payload, controlContext, m.callerRef, m.logger)
	}

	return instanceID, nil

}

//We need to start a goroutine which will act as a fake function and calls the ready endpoint since this
//control flow is client side controlled, e.g. the function connects to the server and asks for new tasks
//We needed to pass a reference to the caller Server in order to be able to access the grpc endpoint without
//having to deal with networking (which is kinda the whole point if the fake runtime)

func (m *MockRuntime) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	return nil, nil //Also not implemented in docker Runtime
}

func (m *MockRuntime) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) { //Currently the instance will still finish running
	for image, list := range m.Running {
		for _, instance := range list {
			if instance.id == req.Id {
				m.logger.Info("Stopping running instance", "id", instance)
				instance.ctx.Done() //this should stop the goroutine
				deleteFromList(m.Running[image], instance.id)
				return &common.InstanceID{Id: instance.id}, nil
			}
		}
	}

	return nil, fmt.Errorf("no such instance: %v", req.Id)
}

func (m *MockRuntime) Status(req *controller.StatusRequest, stream controller.Controller_StatusServer) error {

	return nil //What to do here, also not implemented for docker runtime
}

func (m *MockRuntime) NotifyCrash(ctx context.Context, instanceId *common.InstanceID) error {

	select {} //Just runs forever since we don't need this functionality
}

func (m *MockRuntime) ContainerStats(ctx context.Context, containerID string) io.ReadCloser {
	return nil
}

func fakeEchoFunction(payload *pb.Payload, ctx context.Context, callerRef *caller.CallerServer, logger *slog.Logger) {

	for {
		select {
		case <-ctx.Done():
			return
		default:
			call, err := callerRef.Ready(ctx, payload)
			if err != nil {
				logger.Error("Calling ready failed", "error", err)
				panic("call to ready failed, this should never happen")
			}

			data := call.Data

			payload = &pb.Payload{
				Data:           data,
				InstanceId:     payload.InstanceId,
				Error:          nil,
				FirstExecution: false,
			}
		}
	}

}

func fakeHelloFunction(payload *pb.Payload, ctx context.Context, callerRef *caller.CallerServer, logger *slog.Logger) {

	logger.Debug("Using ID", "id", payload.InstanceId)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, err := callerRef.Ready(ctx, payload)
			if errors.Is(err, context.Canceled) {
				logger.Debug("Context was canceled and function shut down")
			}
			if err != nil {
				logger.Error("Calling ready failed", "error", err)
				panic("call to ready failed, this should never happen")
			}

			payload = &pb.Payload{
				Data:           []byte("Hello World"),
				InstanceId:     payload.InstanceId,
				Error:          nil,
				FirstExecution: false,
			}
		}
	}

}

func deleteFromList(list []RunningInstance, item string) []RunningInstance {
	result := make([]RunningInstance, 1)
	for _, v := range list {
		if v.id != item {
			result = append(result, v)
		}
	}
	return result
}
