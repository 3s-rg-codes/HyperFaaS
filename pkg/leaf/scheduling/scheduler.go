package scheduling

import (
	"context"
	"sync"
	"time"
)

type Scheduler interface {
	// A scheduling decision.
	// A functionID needs an available instance because there is a request for it.
	// The scheduler will return the SchedulingDecision, which just maps functionIDs to workerIDs.
	// That is all we need for now to know where to send the call.
	// Scaling down can potentially also be done here.

	// We will probably want to batch calls to schedule as this computation could become expensive?
	// Something like:
	// Schedule(ctx context.Context, functionIDs []string, state WorkerFunctions) (WorkerFunctions, error)
	Schedule(ctx context.Context, functionID string, state WorkerStateMap) (SchedulingDecision, error)
	UpdateState(ctx context.Context, state WorkerStateMap) error
}

// WorkerStateMap maps worker IDs to their registered functions
type WorkerStateMap map[string][]FunctionState

// SchedulingDecision maps function IDs to the worker ID that should run it
type SchedulingDecision map[string]string

// FunctionState represents the state of a function and its instances
type FunctionState struct {
	FunctionID string
	Running    []InstanceState
	Idle       []InstanceState
}

// InstanceState represents the state of a single function instance
type InstanceState struct {
	InstanceID        string
	IsActive          bool
	TimeSinceLastWork time.Duration //time since the last request was processed to know if to kill
	Uptime            time.Duration //time since the instance was started to know if to kill
}

type naiveScheduler struct {
	workers WorkerStateMap
	mu      sync.Mutex
}

func NewNaiveScheduler() *naiveScheduler {
	return &naiveScheduler{workers: make(WorkerStateMap)}
}

func (s *naiveScheduler) UpdateState(ctx context.Context, state WorkerStateMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers = state
	return nil
}

func (s *naiveScheduler) Schedule(ctx context.Context, functionID string, state WorkerStateMap) (SchedulingDecision, error) {
	// copy the state
	decision := make(SchedulingDecision)

	for workerID, functions := range state {

		for _, function := range functions {

			// The function is registered on the worker
			if function.FunctionID == functionID {
				// There is an idle instance on the worker
				if len(function.Idle) > 0 {
					decision[functionID] = workerID
				}
			}
		}
	}

	return decision, nil
}
