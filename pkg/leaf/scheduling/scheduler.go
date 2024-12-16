package scheduling

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"math/rand"
)

type Scheduler interface {
	// A scheduling decision.
	// A functionID needs an available instance because there is a request for it.
	// The scheduler will return the SchedulingDecision, which just maps functionIDs to workerIPs.
	// That is all we need for now to know where to send the call.
	// Scaling down can potentially also be done here.

	// We will probably want to batch calls to schedule as this computation could become expensive?
	// Something like:
	// Schedule(ctx context.Context, functionIDs []string, state WorkerFunctions) (WorkerFunctions, error)
	Schedule(ctx context.Context, functionID string, state WorkerStateMap) (SchedulingDecision, error)
	UpdateState(ctx context.Context, state WorkerStateMap) error
}

// WorkerStateMap maps worker IPs to their registered functions
type WorkerStateMap map[string][]FunctionState

// SchedulingDecision maps function IDs to the worker IP that should run it.
// Caveat: if there is an available instance on a worker, the correct endpoint in the worker is Call (uses instanceID)
// If there is no available instance, the correct endpoint in the worker would be Start (uses ImageTag currently)
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
	workers   WorkerStateMap
	mu        sync.Mutex
	workerIPs []string
	logger    *slog.Logger
}

func NewNaiveScheduler(workerIPs []string, logger *slog.Logger) *naiveScheduler {
	return &naiveScheduler{workers: make(WorkerStateMap), workerIPs: workerIPs, logger: logger}
}

func (s *naiveScheduler) UpdateState(ctx context.Context, state WorkerStateMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers = state
	s.logger.Debug("Updated worker state", "state", state)
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
					s.logger.Debug("Scheduled function", "functionID", functionID, "workerID", workerID)
					return decision, nil
				}
			}
		}
	}

	// If we get here, there is no available instance on any worker
	// TODO: pick worker with lowest load , for now just a random worker ip
	decision[functionID] = s.workerIPs[rand.Intn(len(s.workerIPs))]
	s.logger.Info("No instance found, scheduling to random worker", "functionID", functionID, "workerID", decision[functionID])

	return decision, nil
}
