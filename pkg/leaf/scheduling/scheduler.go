package scheduling

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

type Scheduler interface {
	// Schedule returns a worker and instance ID for a function where it can be scheduled.
	Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error)
	UpdateWorkerState(workerID state.WorkerID, newState state.WorkerState) error
	UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error
	CreateFunction(workerID state.WorkerID, functionID state.FunctionID) error
}

// New creates a new scheduler based on the strategy
func New(strategy string, workerState *state.Workers, workerIDs []state.WorkerID, logger *slog.Logger) Scheduler {
	switch strategy {
	case "map":
		return NewSyncMapScheduler(workerIDs, logger)
	case "mru":
		return NewMRUScheduler(workerState, workerIDs, logger)
	default:
		return nil
	}
}

// syncMapScheduler uses a nested sync.Map to store the worker state
type syncMapScheduler struct {
	workers   state.WorkersSyncMap
	workerIDs []state.WorkerID
	logger    *slog.Logger
}

func NewSyncMapScheduler(workerIDs []state.WorkerID, logger *slog.Logger) *syncMapScheduler {
	//TODO find a way to pass in a starting state...
	workers := state.NewWorkersSyncMap(logger)
	for _, workerID := range workerIDs {
		workers.CreateWorker(workerID)
	}
	r := NewReconciler(workerIDs, workers, logger)
	go r.Run(context.Background())

	go func() {
		for {
			time.Sleep(10 * time.Second)
			workers.DebugPrint()
		}
	}()
	return &syncMapScheduler{workers: *workers, workerIDs: workerIDs, logger: logger}
}

// Schedule finds an idle instance in a worker for a function.
// If one is found, state is updated to running internally.
func (s *syncMapScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	workerID, instanceID, err := s.workers.FindIdleInstance(functionID)
	if err != nil {
		// TODO: pick worker with lowest load
		workerID = s.workerIDs[rand.Intn(len(s.workerIDs))]

		switch e := err.(type) {
		case *state.FunctionNotAssignedError:
			s.logger.Info("Function not assigned to any worker, creating on random worker", "functionID", functionID, "workerID", workerID)
			s.workers.AssignFunction(workerID, functionID) //Before s.CreateFunction(workerID, functionID) where we only call s.workers.CreateFunction (which is now AssignFunction) cause we had CreateFunction three times
		case *state.NoIdleInstanceError:
			s.logger.Info("No idle instance found, scheduling to random worker", "functionID", functionID, "workerID", workerID)
		default:
			s.logger.Error("Unexpected error type", "error", e)
		}
		return workerID, "", nil
	}
	s.logger.Debug("Found idle instance", "functionID", functionID, "workerID", workerID, "instanceID", instanceID)
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
	//TODO handle errors
	case state.InstanceStateRunning:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateRunning, state.Instance{InstanceID: instanceID})
	case state.InstanceStateIdle:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateIdle, state.Instance{InstanceID: instanceID, LastWorked: time.Now()})
	case state.InstanceStateNew:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateNew, state.Instance{InstanceID: instanceID, LastWorked: time.Now(), Created: time.Now()})
	case state.InstanceStateDown:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateDown, state.Instance{InstanceID: instanceID})
	}
	return nil
}

func (s *syncMapScheduler) CreateFunction(workerID state.WorkerID, functionID state.FunctionID) error {
	//Why does this exist?
	s.workers.AssignFunction(workerID, functionID)
	return nil
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

// Schedule returns a worker and the most recently used instance ID for a function where it can be scheduled.
func (s *mruScheduler) Schedule(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error) {
	workerID, instanceID, err := s.workers.FindIdleInstance(functionID)
	if err != nil {
		// TODO: pick worker with lowest load
		workerID = s.workerIDs[rand.Intn(len(s.workerIDs))]

		switch e := err.(type) {
		case *state.FunctionNotAssignedError:
			s.logger.Info("Function not registered, creating on random worker", "functionID", functionID, "workerID", workerID)
			s.workers.AssignFunction(workerID, functionID)
		case *state.NoIdleInstanceError:
			s.logger.Info("No idle instance found, scheduling to random worker", "functionID", functionID, "workerID", workerID)
		default:
			s.logger.Error("Unexpected error type", "error", e)
		}
		return workerID, "", nil
	}
	s.logger.Debug("Found idle instance", "functionID", functionID, "workerID", workerID, "instanceID", instanceID)
	// UpdateInstance to running
	s.UpdateInstanceState(workerID, functionID, instanceID, state.InstanceStateRunning)
	return workerID, instanceID, nil
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

func (s *mruScheduler) UpdateInstanceState(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, newState state.InstanceState) error {
	switch newState {
	case state.InstanceStateRunning:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateRunning, state.Instance{InstanceID: instanceID})
	case state.InstanceStateIdle:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateIdle, state.Instance{InstanceID: instanceID, LastWorked: time.Now()})
	case state.InstanceStateNew:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateNew, state.Instance{InstanceID: instanceID, LastWorked: time.Now(), Created: time.Now()})
	case state.InstanceStateDown:
		s.workers.UpdateInstance(workerID, functionID, state.InstanceStateDown, state.Instance{InstanceID: instanceID})
	}
	return nil
}

func (s *mruScheduler) CreateFunction(workerID state.WorkerID, functionID state.FunctionID) error {
	s.workers.AssignFunction(workerID, functionID) //see above
	return nil
}
