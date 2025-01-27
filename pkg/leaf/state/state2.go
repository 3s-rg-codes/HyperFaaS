package state

import (
	"fmt"
	"log/slog"
	"sync"
)

// Function represents a function and its instances
type Function2 struct {
	Instances map[InstanceState]map[InstanceID]Instance
	sync.RWMutex
}

// Worker represents a worker and its functions
type Worker struct {
	Functions map[FunctionID]*Function2
	sync.RWMutex
}

// Workers is the main struct that manages all workers, functions, and instances
type Workers2 struct {
	workers map[WorkerID]*Worker
	sync.RWMutex
	logger *slog.Logger
}

// NewWorkers creates a new Workers instance
func NewWorkers2(logger *slog.Logger) *Workers2 {
	return &Workers2{
		workers: make(map[WorkerID]*Worker),
		logger:  logger,
	}
}

// CreateWorker creates a new worker
func (w *Workers2) CreateWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	w.workers[workerID] = &Worker{
		Functions: make(map[FunctionID]*Function2),
	}
}

// DeleteWorker deletes a worker
func (w *Workers2) DeleteWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	delete(w.workers, workerID)
}

// GetWorker retrieves a worker by ID
func (w *Workers2) GetWorker(workerID WorkerID) (*Worker, error) {
	w.RLock()
	defer w.RUnlock()
	worker, ok := w.workers[workerID]
	if !ok {
		return nil, fmt.Errorf("worker not found: %v", workerID)
	}
	return worker, nil
}

// CreateFunction creates a new function for a worker
func (w *Workers2) CreateFunction(workerID WorkerID, functionID FunctionID) error {
	worker, err := w.GetWorker(workerID)
	if err != nil {
		return err
	}

	worker.Lock()
	defer worker.Unlock()
	worker.Functions[functionID] = &Function2{
		Instances: make(map[InstanceState]map[InstanceID]Instance),
	}
	return nil
}

// GetFunction retrieves a function by ID
func (w *Workers2) GetFunction(workerID WorkerID, functionID FunctionID) (*Function2, error) {
	worker, err := w.GetWorker(workerID)
	if err != nil {
		return nil, err
	}

	worker.RLock()
	defer worker.RUnlock()
	function, ok := worker.Functions[functionID]
	if !ok {
		return nil, fmt.Errorf("function not found: %v", functionID)
	}
	return function, nil
}

// AddInstance adds an instance to a function's state
func (f *Function2) AddInstance(state InstanceState, instance Instance) {
	f.Lock()
	defer f.Unlock()
	if f.Instances[state] == nil {
		f.Instances[state] = make(map[InstanceID]Instance)
	}
	f.Instances[state][instance.InstanceID] = instance
}

// RemoveInstance removes an instance from a function's state
func (f *Function2) RemoveInstance(state InstanceState, instanceID InstanceID) {
	f.Lock()
	defer f.Unlock()
	delete(f.Instances[state], instanceID)
}

// MoveInstance moves an instance from one state to another
func (f *Function2) MoveInstance(fromState, toState InstanceState, instanceID InstanceID) error {
	f.Lock()
	defer f.Unlock()

	instance, ok := f.Instances[fromState][instanceID]
	if !ok {
		return fmt.Errorf("instance not found in state %v: %v", fromState, instanceID)
	}

	// Remove from the old state
	delete(f.Instances[fromState], instanceID)

	// Add to the new state
	if f.Instances[toState] == nil {
		f.Instances[toState] = make(map[InstanceID]Instance)
	}
	f.Instances[toState][instanceID] = instance

	return nil
}

// UpdateInstance updates the state of an instance
func (w *Workers2) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	switch instanceState {
	case InstanceStateNew:
		function.AddInstance(InstanceStateRunning, instance)
	case InstanceStateIdle:
		if err := function.MoveInstance(InstanceStateRunning, InstanceStateIdle, instance.InstanceID); err != nil {
			return err
		}
	case InstanceStateRunning:
		if err := function.MoveInstance(InstanceStateIdle, InstanceStateRunning, instance.InstanceID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid instance state: %v", instanceState)
	}

	w.logger.Debug("Updated instance", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance)
	return nil
}

// DeleteInstance deletes an instance from a function's state
func (w *Workers2) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	function.RemoveInstance(instanceState, instanceID)
	return nil
}

// FindIdleInstance finds an idle instance for a function
func (w *Workers2) FindIdleInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
	w.RLock()
	defer w.RUnlock()
	functionExists := false
	for workerID, worker := range w.workers {
		worker.RLock()
		function, ok := worker.Functions[functionID]
		worker.RUnlock()

		if !ok {
			continue
		}
		functionExists = true
		function.RLock()
		idleInstances := function.Instances[InstanceStateIdle]
		function.RUnlock()

		for instanceID := range idleInstances {
			// Move the instance to the running state
			if err := w.UpdateInstance(workerID, functionID, InstanceStateRunning, idleInstances[instanceID]); err != nil {
				return "", "", err
			}
			return workerID, instanceID, nil
		}
	}

	if !functionExists {
		return "", "", &FunctionNotRegisteredError{FunctionID: functionID}
	}

	return "", "", &NoIdleInstanceError{FunctionID: functionID}
}

// DebugPrint prints the current state of all workers, functions, and instances
func (w *Workers2) DebugPrint() {
	w.RLock()
	defer w.RUnlock()

	for workerID, worker := range w.workers {
		w.logger.Debug("Worker", "id", workerID)
		worker.RLock()
		for functionID, function := range worker.Functions {
			w.logger.Debug("Function", "id", functionID)
			function.RLock()
			for state, instances := range function.Instances {
				w.logger.Debug("Instances", "state", state, "count", len(instances))
				for instanceID, instance := range instances {
					w.logger.Debug("Instance", "id", instanceID, "created", instance.Created, "lastWorked", instance.LastWorked)
				}
			}
			function.RUnlock()
		}
		worker.RUnlock()
	}
}
