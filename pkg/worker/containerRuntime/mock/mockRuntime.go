package mock

/*
import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MockRuntime struct {
	cr.ContainerRuntime
	logger  *slog.Logger
	mapLock *sync.Mutex
	Running map[string][]RunningInstance
	Dict    map[string]string
}

type RunningInstance struct {
	ctx context.Context
	id  string
}

func NewMockRuntime(logger *slog.Logger) *MockRuntime {
	return &MockRuntime{
		logger:  logger,
		mapLock: new(sync.Mutex),
		Running: make(map[string][]RunningInstance),
		Dict:    make(map[string]string),
	}
}

func (m *MockRuntime) Start(ctx context.Context, functionID string, imageTag string, _ *common.Config) (string, error) {

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
	m.mapLock.Lock()
	m.Running[imageTag] = append(m.Running[imageTag], instance)
	m.mapLock.Unlock()
	payload := &pb.Payload{
		FirstExecution: true,
		InstanceId:     &common.InstanceID{Id: instanceID},
		FunctionId:     &common.FunctionID{Id: functionID},
	}
	switch imageTag {
	case "hyperfaas-hello:latest":
		go fakeHelloFunction(payload, controlContext, m.callerRef, m.logger)
	case "hyperfaas-echo:latest":

		go fakeEchoFunction(payload, controlContext, m.callerRef, m.logger)
	case "luccadibe/hyperfaas-functions:hello":
		go fakeHelloFunction(payload, controlContext, m.callerRef, m.logger)
	default:
		return "", status.Errorf(codes.NotFound, "image tag is not available for mock runtime, %v", imageTag)
	}

	return instanceID, nil

}

//We need to start a goroutine which will act as a fake function and calls the ready endpoint since this
//control flow is client side controlled, e.g. the function connects to the server and asks for new tasks
//We needed to pass a reference to the caller Server in order to be able to access the grpc endpoint without
//having to deal with networking (which is kinda the whole point of the fake runtime)

func (m *MockRuntime) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	return nil, nil //Also not implemented in docker Runtime
}

func (m *MockRuntime) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) { //Currently the instance will still finish running
	for image, list := range m.Running {
		for _, instance := range list {
			m.logger.Debug("Instance for Image", "image", image, "instance", instance.id)
		}
	}

	m.logger.Debug("Trying to stop instance", "instanceId", req.Id)

	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for image, list := range m.Running {
		for _, instance := range list {
			if instance.id == req.Id {
				m.logger.Info("Stopping running instance", "id", instance.id)
				instance.ctx.Done() //this should stop the goroutine
				m.Running[image] = deleteFromList(m.Running[image], instance.id)
				if len(m.Running[image]) == 0 {
					delete(m.Running, image)
				}
				return &common.InstanceID{Id: instance.id}, nil
			}
		}
	}
	m.logger.Warn("No such instance running", "id", req.Id)
	return nil, status.Errorf(codes.NotFound, "no such instance running %v", req.Id)
}

func (m *MockRuntime) Status(req *controller.StatusRequest, stream controller.Controller_StatusServer) error {

	return nil //What to do here, also not implemented for docker runtime
}

func (m *MockRuntime) NotifyCrash(ctx context.Context, instanceId *common.InstanceID) error {
	return nil
}

func (m *MockRuntime) ContainerStats(ctx context.Context, containerID string) io.ReadCloser {
	return nil
}

func (m *MockRuntime) RemoveImage(ctox context.Context, imageID string) error {
	return nil //Also dont need this
}

func (m *MockRuntime) ContainerExists(ctx context.Context, instanceID string) bool {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for _, list := range m.Running {
		for _, val := range list {
			if val.id == instanceID {
				return false
			}
		}
	}
	return true
}

func fakeEchoFunction(payload *pb.Payload, ctx context.Context, callerRef *caller.CallerServer, logger *slog.Logger) {
	parentCtx := ctx
	for {
		// Create new timeout context for each iteration
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)

		select {
		case <-parentCtx.Done():
			cancel()
			return
		default:
			call, err := callerRef.Ready(ctx, payload)
			cancel()

			if errors.Is(err, context.Canceled) {
				logger.Debug("Context was canceled and function shut down")
			}
			st, _ := status.FromError(err)
			if st.Code() == codes.NotFound || st.Code() == codes.Unavailable {
				logger.Debug("channel was closed before context could be canceled")
				return
			}
			if err != nil {
				logger.Warn("Calling ready failed", "error", err)
				return
			}
			if ctx.Err() == context.DeadlineExceeded {
				// Timeout occurred, don't try to send response
				logger.Debug("Ready call timed out, stopping function")
				return
			}
			if call.Data == nil {
				return
			}
			data := call.Data

			payload = &pb.Payload{
				Data:           data,
				InstanceId:     payload.InstanceId,
				FunctionId:     payload.FunctionId,
				Error:          nil,
				FirstExecution: false,
			}
		}
	}

}

func fakeHelloFunction(payload *pb.Payload, ctx context.Context, callerRef *caller.CallerServer, logger *slog.Logger) {
	parentCtx := ctx
	for {
		// Create new timeout context for each iteration
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)

		select {
		case <-parentCtx.Done():
			cancel()
			return
		default:
			_, err := callerRef.Ready(ctx, payload)
			cancel()
			if errors.Is(err, context.Canceled) {
				logger.Debug("Context was canceled and function shut down")
				return
			}
			st, _ := status.FromError(err)
			if st.Code() == codes.NotFound || st.Code() == codes.Unavailable {
				logger.Debug("channel was closed before context could be canceled")
				return
			}
			if err != nil {
				logger.Warn("Calling ready failed", "error", err)
				return
			}
			if ctx.Err() == context.DeadlineExceeded {
				// Timeout occurred, don't try to send response
				logger.Debug("Ready call timed out, stopping function")
				return
			}

			payload = &pb.Payload{
				Data:           []byte("HELLO WORLD!"),
				InstanceId:     payload.InstanceId,
				FunctionId:     payload.FunctionId,
				Error:          nil,
				FirstExecution: false,
			}

		}
	}
}

func deleteFromList(list []RunningInstance, item string) []RunningInstance {
	result := make([]RunningInstance, 0)
	for _, v := range list {
		if v.id != item {
			result = append(result, v)
		}
	}
	return result
}
*/
