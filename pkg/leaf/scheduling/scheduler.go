package scheduling

import (
	"context"
	"log/slog"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
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
	Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error)
	//TODO divide into:
	UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error
	UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error
}

type naiveScheduler struct {
	workerState state.WorkerStateMap
	mu          sync.Mutex
	workerIDs   []state.WorkerID
	logger      *slog.Logger
}

func New(strategy string, workerState state.WorkerStateMap, workerIDs []state.WorkerID, logger *slog.Logger) Scheduler {
	switch strategy {
	case "naive":
		return NewNaiveScheduler(workerState, workerIDs, logger)
	case "mru":
		return NewMRUScheduler(workerState, workerIDs, logger)
	case "map":
		return NewSyncMapScheduler(workerIDs, logger)
	default:
		return nil
	}
}

func NewNaiveScheduler(workerState state.WorkerStateMap, workerIDs []state.WorkerID, logger *slog.Logger) *naiveScheduler {
	return &naiveScheduler{workerState: workerState, workerIDs: workerIDs, logger: logger}
}

func (s *naiveScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	var worker state.WorkerID
	for workerID, functions := range s.workerState {

		for _, function := range functions {

			// The function is registered on the worker
			if function.FunctionID == functionID {
				// There is an idle instance on the worker
				if len(function.Idle) > 0 {
					s.logger.Debug("Scheduled function", "functionID", functionID, "workerID", workerID)
					// return the first idle instance in the list
					return workerID, state.InstanceID(function.Idle[0].InstanceID), nil
				}
			}
		}
	}

	// If we get here, there is no available instance on any worker
	worker = s.workerIDs[rand.Intn(len(s.workerIDs))]
	s.logger.Info("No instance found, scheduling to random worker", "functionID", functionID, "workerID", worker)
	// Union types would be nice here...
	return worker, state.InstanceID(""), nil
}
func (s *naiveScheduler) UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch newState {
	case state.WorkerStateUp:
		// Add worker
		s.workerIDs = append(s.workerIDs, workerID)
		s.workerState[workerID] = []state.Function{}
	case state.WorkerStateDown:
		// Remove worker
		delete(s.workerState, workerID)
		// Remove worker from workerIDs
		// TODO: performance optimization
		for i, id := range s.workerIDs {
			if id == workerID {
				s.workerIDs = append(s.workerIDs[:i], s.workerIDs[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (s *naiveScheduler) UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch newState {
	// From Idle to Running
	case state.InstanceStateRunning:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Running = append(function.Running, state.Instance{InstanceID: instanceID})
				s.workerState[workerID][i].Idle = append(s.workerState[workerID][i].Idle[:i], s.workerState[workerID][i].Idle[i+1:]...)
				break
			}
		}
		// From Running to Idle
	case state.InstanceStateIdle:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Idle = append(function.Idle, state.Instance{InstanceID: instanceID})
				s.workerState[workerID][i].Running = append(s.workerState[workerID][i].Running[:i], s.workerState[workerID][i].Running[i+1:]...)
				break
			}
		}
		// A new instance is created - always running at start
	case state.InstanceStateNew:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Running = append(function.Running, state.Instance{InstanceID: instanceID})
				break
			}
		}
	}
	return nil
}

type mruScheduler struct {
	workerState state.WorkerStateMap
	mu          sync.Mutex
	workerIDs   []state.WorkerID
	logger      *slog.Logger
}

func NewMRUScheduler(workerState state.WorkerStateMap, workerIDs []state.WorkerID, logger *slog.Logger) *mruScheduler {
	return &mruScheduler{workerState: workerState, workerIDs: workerIDs, logger: logger}
}

func (s *mruScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	var worker state.WorkerID

	for workerID, functions := range s.workerState {

		for _, function := range functions {
			if function.FunctionID != functionID || len(function.Idle) == 0 {
				continue
			}

			sort.Slice(function.Idle, func(i, j int) bool {
				return function.Idle[i].LastWorked.Before(function.Idle[j].LastWorked)
			})

			worker = state.WorkerID(workerID)
			s.logger.Debug("Scheduled function", "functionID", functionID, "workerID", workerID, "instanceID", function.Idle[0].InstanceID)
			return worker, state.InstanceID(function.Idle[0].InstanceID), nil
		}
	}

	// If we get here, there is no available instance on any worker
	// TODO: pick worker with lowest load , for now just a random worker ip
	worker = s.workerIDs[rand.Intn(len(s.workerIDs))]
	s.logger.Info("No instance found, scheduling to random worker", "functionID", functionID, "workerID", worker)

	return worker, state.InstanceID(""), nil
}
func (s *mruScheduler) UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch newState {
	case state.WorkerStateUp:
		// Add worker
		s.workerIDs = append(s.workerIDs, workerID)
		s.workerState[workerID] = []state.Function{}
	case state.WorkerStateDown:
		// Remove worker
		delete(s.workerState, workerID)
		// Remove worker from workerIDs
		// TODO: performance optimization
		for i, id := range s.workerIDs {
			if id == workerID {
				s.workerIDs = append(s.workerIDs[:i], s.workerIDs[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (s *mruScheduler) UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch newState {
	// From Idle to Running
	case state.InstanceStateRunning:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Running = append(function.Running, state.Instance{InstanceID: instanceID})
				s.workerState[workerID][i].Idle = append(s.workerState[workerID][i].Idle[:i], s.workerState[workerID][i].Idle[i+1:]...)
				break
			}
		}
		// From Running to Idle
	case state.InstanceStateIdle:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Idle = append(function.Idle, state.Instance{InstanceID: instanceID})
				s.workerState[workerID][i].Running = append(s.workerState[workerID][i].Running[:i], s.workerState[workerID][i].Running[i+1:]...)
				break
			}
		}
		// A new instance is created - always running at start
	case state.InstanceStateNew:
		for i, function := range s.workerState[workerID] {
			if function.FunctionID == functionID {
				s.workerState[workerID][i].Running = append(function.Running, state.Instance{InstanceID: instanceID})
				break
			}
		}
	}
	return nil
}

type syncMapScheduler struct {
	workers   state.Workers
	workerIDs []state.WorkerID
	logger    *slog.Logger
}

func NewSyncMapScheduler(workerIDs []state.WorkerID, logger *slog.Logger) *syncMapScheduler {
	//TODO find a way to pass in a starting state...
	workers := state.NewWorkers(logger)
	for _, workerID := range workerIDs {
		workers.CreateWorker(workerID)
	}
	return &syncMapScheduler{workers: *workers, workerIDs: workerIDs, logger: logger}
}

// Finds an idle instance in a worker for a function.
// If one is found, state is updated to running internally.
func (s *syncMapScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	workerID, instanceID, err := s.workers.FindIdleInstance(functionID)
	if err != nil {
		// No idle instance found
		// TODO: pick worker with lowest load
		workerID = s.workerIDs[rand.Intn(len(s.workerIDs))]
		s.logger.Info("No idle instance found, scheduling to random worker", "functionID", functionID, "workerID", workerID)
		return workerID, "", nil
	}
	return workerID, instanceID, nil
}

func (s *syncMapScheduler) UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error {
	switch newState {
	case state.WorkerStateUp:
		s.workers.CreateWorker(workerID)
	case state.WorkerStateDown:
		s.workers.DeleteWorker(workerID)
	}
	return nil
}

func (s *syncMapScheduler) UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error {
	switch newState {
	case state.InstanceStateRunning:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateRunning, state.Instance{InstanceID: instanceID, LastWorked: time.Now()})
	case state.InstanceStateIdle:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateIdle, state.Instance{InstanceID: instanceID, LastWorked: time.Now()})
	case state.InstanceStateNew:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateNew, state.Instance{InstanceID: instanceID, LastWorked: time.Now(), Created: time.Now()})
	}
	return nil
}

func (s *syncMapScheduler) CreateFunction(workerID state.WorkerID, functionID state.FunctionID) error {
	s.workers.CreateFunction(workerID, functionID)
	return nil
}
