package mock

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ cr.ContainerRuntime = &MockRuntime{}

type MockRuntime struct {
	logger       *slog.Logger
	mapLock      *sync.RWMutex
	Instances    map[string][]*instance
	readySignals *controller.ReadySignals
}

// ContainerExists implements containerRuntime.ContainerRuntime.
func (m *MockRuntime) ContainerExists(ctx context.Context, instanceID string) bool {
	m.mapLock.RLock()
	defer m.mapLock.RUnlock()
	for _, list := range m.Instances {
		for _, val := range list {
			if val.id == instanceID {
				return false
			}
		}
	}
	return true
}

// ContainerStats doesnt make sense for mock runtime
func (m *MockRuntime) ContainerStats(ctx context.Context, containerID string) io.ReadCloser {
	// maybe doesn't make sense for mock runtime ..?
	// TODO
	panic("not implemented")
}

// MonitorContainer implements containerRuntime.ContainerRuntime.
func (m *MockRuntime) MonitorContainer(ctx context.Context, instanceId string, functionId string) (cr.ContainerEvent, error) {
	m.mapLock.RLock()
	instances, ok := m.Instances[functionId]
	m.mapLock.RUnlock()
	if !ok {
		// unsure if this is the correct return value, but we dont have any other information to return
		return cr.ContainerEventExit, fmt.Errorf("function %s not found", functionId)
	}
	for _, instance := range instances {
		if instance.id == instanceId {
			// just wait for the context to be done
			<-instance.ctx.Done()
			return cr.ContainerEventTimeout, nil
		}
	}
	return cr.ContainerEventExit, fmt.Errorf("instance %s not found", instanceId)
}

// RemoveImage doesnt make sense for mock runtime
func (m *MockRuntime) RemoveImage(ctx context.Context, imageID string) error {
	panic("not implemented")
}

// Start implements containerRuntime.ContainerRuntime.
func (m *MockRuntime) Start(ctx context.Context, functionID string, imageTag string, config *commonpb.Config) (cr.Container, error) {
	longID, err := uuid.NewUUID()
	if err != nil {
		return cr.Container{}, err
	}
	instanceID := longID.String()[:8] // truncating the ID to 8 for compatibility

	var handler handler
	switch imageTag {
	case "hyperfaas-hello:latest":
		handler = &fakeHelloFunction{}
	case "hyperfaas-echo:latest":
		handler = &fakeEchoFunction{}
	case "hyperfaas-simul:latest":
		handler = &fakeSimulFunction{}
	default:
		return cr.Container{}, status.Errorf(codes.NotFound, "image tag is not available for mock runtime, %v", imageTag)
	}

	fnCtx, fnCancel := context.WithCancel(ctx)
	m.mapLock.Lock()
	i := &instance{
		ctx:          fnCtx,
		cancel:       fnCancel,
		id:           instanceID,
		timeout:      time.Duration(config.Timeout) * time.Second,
		lastActivity: time.Now(),
		handler:      handler,
	}
	m.Instances[functionID] = append(m.Instances[functionID], i)
	m.mapLock.Unlock()

	// Signal that the instance is ready. If we dont do this, the controller will wait forever for the instance to be ready.
	m.readySignals.AddInstance(instanceID)
	go m.readySignals.SignalReady(instanceID)

	go i.monitorTimeout()

	return cr.Container{Id: instanceID, Name: imageTag, IP: "MOCK_RUNTIME_NO_IP"}, nil
}

// Stop implements containerRuntime.ContainerRuntime.
func (m *MockRuntime) Stop(ctx context.Context, instanceID string) error {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for _, instance := range m.Instances[instanceID] {
		instance.cancel()
	}
	delete(m.Instances, instanceID)
	return nil
}

type instance struct {
	ctx          context.Context
	cancel       context.CancelFunc
	id           string
	timeout      time.Duration
	lastActivity time.Time
	activityMu   sync.RWMutex
	// used to implement the function custom logic
	handler handler
}

func NewMockRuntime(logger *slog.Logger, readySignals *controller.ReadySignals) *MockRuntime {
	return &MockRuntime{
		logger:       logger,
		mapLock:      new(sync.RWMutex),
		Instances:    make(map[string][]*instance),
		readySignals: readySignals,
	}
}

type handler interface {
	HandleCall(ctx context.Context, req *commonpb.CallRequest) (*commonpb.CallResponse, error)
}

// monitorTimeout monitors the instance's last activity and cancels the context if the instance has timed out
// implementation is almos identical to the functionRuntimeInterface's monitorTimeout
func (f *instance) monitorTimeout() {
	ticker := time.NewTicker(time.Second)

	for range ticker.C {
		select {
		// could be called from Stop()
		case <-f.ctx.Done():
			return
		default:
			f.activityMu.RLock()
			timeSinceLastActivity := time.Since(f.lastActivity)
			f.activityMu.RUnlock()

			if timeSinceLastActivity >= f.timeout {
				f.cancel()
				return
			}
		}
	}
}
