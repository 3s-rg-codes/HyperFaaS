package scheduling

import (
	"context"
	"log/slog"
	"math/rand"
	"sort"
	"sync"

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
	// TODO refactor this to use StateUpdates from workers. Also not really sure if this should return an error.
	UpdateState(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) error
	//TODO divide into:
	UpdateWorkerState(workerID state.WorkerID, <some kind of enum?>) error
	UpdateInstanceState(workerID state.WorkerID, instanceID state.InstanceID, functionID state.FunctionID, <some kind of enum?>) error
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
	default:
		return nil
	}
}

func NewNaiveScheduler(workerState state.WorkerStateMap, workerIDs []state.WorkerID, logger *slog.Logger) *naiveScheduler {
	return &naiveScheduler{workerState: workerState, workerIDs: workerIDs, logger: logger}
}

func (s *naiveScheduler) UpdateState(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Find the index of the function we want to modify
	for i, function := range s.workerState[workerID] {
		if function.FunctionID == functionID {
			// Find the idle instance and remove it
			for j, instance := range function.Idle {
				if instance.InstanceID == instanceID {
					// Function is no longer idle, remove it from the list
					s.workerState[workerID][i].Idle = append(function.Idle[:j], function.Idle[j+1:]...)
					break
				}
			}
			// add it to the running list
			s.workerState[workerID][i].Running = append(function.Running, state.InstanceState{InstanceID: instanceID})
			break
		}
	}
	s.logger.Debug("Updated worker state", "workerID", workerID, "functionID", functionID, "instanceID", instanceID)
	return nil
}

func (s *naiveScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	var worker state.WorkerID
	for workerID, functions := range s.workerState {

		for _, function := range functions {

			// The function is registered on the worker
			if function.FunctionID == functionID {
				// There is an idle instance on the worker
				if len(function.Idle) > 0 {
					worker = state.WorkerID(workerID)
					s.logger.Debug("Scheduled function", "functionID", functionID, "workerID", workerID)
					// return the first idle instance in the list
					return worker, state.InstanceID(function.Idle[0].InstanceID), nil
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

type mruScheduler struct {
	workerState state.WorkerStateMap
	mu          sync.Mutex
	workerIDs   []state.WorkerID
	logger      *slog.Logger
}

func NewMRUScheduler(workerState state.WorkerStateMap, workerIDs []state.WorkerID, logger *slog.Logger) *mruScheduler {
	return &mruScheduler{workerState: workerState, workerIDs: workerIDs, logger: logger}
}

func (s *mruScheduler) UpdateState(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Find the index of the function we want to modify
	for i, function := range s.workerState[workerID] {
		if function.FunctionID == functionID {
			// Find the idle instance and remove it
			for j, instance := range function.Idle {
				if instance.InstanceID == instanceID {
					// Function is no longer idle, remove it from the list
					s.workerState[workerID][i].Idle = append(function.Idle[:j], function.Idle[j+1:]...)
					break
				}
			}
			// add it to the running list
			s.workerState[workerID][i].Running = append(function.Running, state.InstanceState{InstanceID: instanceID})
			break
		}
	}
	s.logger.Debug("Updated worker state", "workerID", workerID, "functionID", functionID, "instanceID", instanceID)
	return nil
}

func (s *mruScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	var worker state.WorkerID

	for workerID, functions := range s.workerState {

		for _, function := range functions {
			if function.FunctionID != functionID || len(function.Idle) == 0 {
				continue
			}

			sort.Slice(function.Idle, func(i, j int) bool {
				return function.Idle[i].TimeSinceLastWork < function.Idle[j].TimeSinceLastWork
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
