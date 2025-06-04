package scheduling

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

type Scheduler interface {
	// Schedule returns a worker and instance ID for a function where it can be scheduled.
	// TODO find a cleaner way to pass maxConcurrency or other variables. maybe another struct?
	// the only problem i have with that is that we already have the Function struct in state.go
	Schedule(ctx context.Context, functionID state.FunctionID, maxConcurrency int32) (Decision, error)
	UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error
	ReduceInstanceConcurrency(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID)
	GetInstanceCount(workerID state.WorkerID, functionID state.FunctionID, instanceState state.InstanceState) (int, error)
	CreateFunction(workerID state.WorkerID, functionID state.FunctionID, maxConcurrency int32) error
	AddInstance(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) error
	UpdateStartingInstancesCounter(workerID state.WorkerID, functionID state.FunctionID, delta int32) error
}
type Decision struct {
	WorkerID   state.WorkerID
	InstanceID state.InstanceID
	Decision   SchedulingDecision
}
type SchedulingDecision int

const (
	Schedule SchedulingDecision = iota
	StartInstance
	TooManyRequests
	TooManyStartingInstances
)

// New creates a new scheduler based on the strategy
func New(strategy string, workerState *state.Workers, workerIDs []state.WorkerID, logger *slog.Logger) Scheduler {
	switch strategy {
	case "mru":
		return NewMRUScheduler(workerState, workerIDs, logger)
	default:
		return nil
	}
}

type mruScheduler struct {
	workers    *state.Workers
	workerIDs  []state.WorkerID
	reconciler *Reconciler
	logger     *slog.Logger
}

func NewMRUScheduler(workers *state.Workers, workerIDs []state.WorkerID, logger *slog.Logger) *mruScheduler {
	for _, workerID := range workerIDs {
		workers.CreateWorker(workerID)
	}
	reconciler := NewReconciler(workerIDs, workers, logger)
	go reconciler.Run(context.Background())
	go func() {
		for {
			time.Sleep(10 * time.Second)
			workers.DebugPrint()
		}
	}()

	return &mruScheduler{workers: workers, workerIDs: workerIDs, logger: logger, reconciler: reconciler}

}

// Schedule returns a decision based on the current state of the workers. It can be:
// Schedule: a worker and instance ID for a function where it can be scheduled.
// StartInstance: a worker and instance ID for a function where a new instance should be started.
// TooManyRequests: the function has too many requests.
// TooManyStartingInstances: the function has too many starting instances.
func (s *mruScheduler) Schedule(ctx context.Context, functionID state.FunctionID, maxConcurrency int32) (Decision, error) {
	workerID, instanceID, err := s.workers.FindMRUInstance(functionID)
	if err != nil {
		// TODO: pick worker with lowest load
		workerID = s.workerIDs[rand.Intn(len(s.workerIDs))]

		switch e := err.(type) {
		case *state.FunctionNotAssignedError:
			s.logger.Info("Function not registered, creating on random worker", "functionID", functionID, "workerID", workerID)
			// TODO: get max concurrency from database
			// The check here is because we could have a race condition where many req arrive at once, and they all try to create the function.
			err = s.CreateFunction(workerID, functionID, maxConcurrency)
			if errors.Is(err, &state.FunctionAlreadyAssignedError{}) {
				// WARNING this could go wrong quickly xD
				return s.Schedule(ctx, functionID, maxConcurrency)
			}
		case *state.NoIdleInstanceError:
			s.logger.Info("No idle instance found", "functionID", functionID, "workerID", workerID)
		case *state.TooManyStartingInstancesError:
			return Decision{WorkerID: workerID, InstanceID: "", Decision: TooManyStartingInstances}, nil
		default:
			s.logger.Error("Unexpected error type", "error", e)
		}
		return Decision{WorkerID: workerID, InstanceID: "", Decision: StartInstance}, nil
	}
	s.logger.Debug("Found idle instance", "functionID", functionID, "workerID", workerID, "instanceID", instanceID)
	return Decision{WorkerID: workerID, InstanceID: instanceID, Decision: Schedule}, nil
}

func (s *mruScheduler) UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error {
	switch newState {
	case state.WorkerStateUp:
		s.workers.CreateWorker(workerID)
	case state.WorkerStateDown:
		s.workers.DeleteWorker(workerID)
	}
	return nil
}

func (s *mruScheduler) ReduceInstanceConcurrency(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) {
	s.workers.ReduceInstanceConcurrency(workerID, functionID, instanceID)
}

func (s *mruScheduler) CreateFunction(workerID state.WorkerID, functionID state.FunctionID, maxConcurrency int32) error {
	return s.workers.AssignFunction(workerID, functionID, maxConcurrency)
}

func (s *mruScheduler) GetInstanceCount(workerID state.WorkerID, functionID state.FunctionID, instanceState state.InstanceState) (int, error) {
	return s.workers.CountInstancesInState(workerID, functionID, instanceState)
}

func (s *mruScheduler) AddInstance(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) error {
	return s.workers.AddInstance(workerID, functionID, instanceID)
}

func (s *mruScheduler) UpdateStartingInstancesCounter(workerID state.WorkerID, functionID state.FunctionID, delta int32) error {
	s.workers.UpdateStartingInstancesCounter(workerID, functionID, delta)
	return nil
}
