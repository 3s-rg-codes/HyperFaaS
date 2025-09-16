package mock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

var _ controller.CallRouter = &MockCallRouter{}

// MockCallRouter is a mock implementation of the CallRouter interface. It can be used in combination with the MockRuntime to avoid creating containers and test the controller logic.
type MockCallRouter struct {
	mockRuntime *MockRuntime
	logger      *slog.Logger
	mu          sync.RWMutex
	rrIndex     map[string]int
}

// AddInstance implements controller.CallRouter.
func (m *MockCallRouter) AddInstance(functionID string, ip string) {
}

// CallFunction implements controller.CallRouter.
func (m *MockCallRouter) CallFunction(functionID string, req *common.CallRequest) (*common.CallResponse, error) {
	if m.mockRuntime == nil {
		return nil, errors.New("mock runtime not set")
	}

	m.mu.RLock()
	instances, ok := m.mockRuntime.Instances[functionID]
	if !ok || len(instances) == 0 {
		m.mu.RUnlock()
		return nil, fmt.Errorf("no instances found for function %s", functionID)
	}
	m.mu.RUnlock()

	// round-robin index per function
	m.mu.Lock()
	if m.rrIndex == nil {
		m.rrIndex = make(map[string]int)
	}
	idx := m.rrIndex[functionID] % len(instances)
	m.rrIndex[functionID] = (idx + 1) % len(instances)
	m.mu.Unlock()

	instance := instances[idx]

	resp, err := instance.handler.HandleCall(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// HandleInstanceTimeout implements controller.CallRouter.
func (m *MockCallRouter) HandleInstanceTimeout(functionID string, ip string) {
}

func NewMockCallRouter(logger *slog.Logger, runtime *MockRuntime) *MockCallRouter {
	return &MockCallRouter{logger: logger, mockRuntime: runtime, rrIndex: make(map[string]int)}
}
