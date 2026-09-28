package dataplane

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	"hyperfaas-ideal-arch/pkg/core"
)

// Store is a leaf-local instance registry keyed by function and instance ID.
type Store struct {
	mu sync.RWMutex
	// functionID -> instanceID -> state
	byFunction map[uint64]map[uint64]*core.InstanceState
}

func NewStore() *Store {
	return &Store{
		byFunction: make(map[uint64]map[uint64]*core.InstanceState),
	}
}

func (s *Store) PutInstance(_ context.Context, instance *core.InstanceState) error {
	if instance == nil || instance.GetFunctionId() == 0 || instance.GetInstanceId() == 0 {
		return fmt.Errorf("dataplane store: invalid instance")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	functionID := instance.GetFunctionId()
	if s.byFunction[functionID] == nil {
		s.byFunction[functionID] = make(map[uint64]*core.InstanceState)
	}
	cloned := cloneInstance(instance)
	s.byFunction[functionID][instance.GetInstanceId()] = cloned
	return nil
}

func (s *Store) RemoveInstance(_ context.Context, instanceID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for functionID, instances := range s.byFunction {
		_, ok := instances[instanceID]
		if !ok {
			continue
		}
		delete(instances, instanceID)
		if len(instances) == 0 {
			delete(s.byFunction, functionID)
		}
		return nil
	}
	return nil
}

func (s *Store) RemoveFunction(functionID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byFunction, functionID)
}

func (s *Store) ListReady(functionID uint64) []*core.InstanceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*core.InstanceState, 0, len(s.byFunction[functionID]))
	for _, instance := range s.byFunction[functionID] {
		if instance.GetReady() && !instance.GetStopping() {
			out = append(out, cloneInstance(instance))
		}
	}
	return out
}

func (s *Store) Snapshot() map[uint64][]*core.InstanceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[uint64][]*core.InstanceState, len(s.byFunction))
	for functionID, instances := range s.byFunction {
		list := make([]*core.InstanceState, 0, len(instances))
		for _, inst := range instances {
			list = append(list, cloneInstance(inst))
		}
		out[functionID] = list
	}
	return out
}

// testInstanceCloneHook is set by tests to observe cloneInstance calls.
var testInstanceCloneHook func()

// SetTestInstanceCloneHook registers a hook invoked on each cloneInstance call (tests only).
func SetTestInstanceCloneHook(h func()) {
	testInstanceCloneHook = h
}

func cloneInstance(in *core.InstanceState) *core.InstanceState {
	if in == nil {
		return nil
	}
	if testInstanceCloneHook != nil {
		testInstanceCloneHook()
	}
	return proto.Clone(in).(*core.InstanceState)
}
