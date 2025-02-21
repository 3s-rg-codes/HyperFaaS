package state

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type WorkerID string
type InstanceID string
type FunctionID string

type WorkerState int
type InstanceState int

const (
	WorkerStateUp WorkerState = iota
	WorkerStateDown
)

const (
	InstanceStateRunning InstanceState = iota
	InstanceStateIdle
	InstanceStateNew
	// Only used for instances that were running and crashed / their worker is down
	InstanceStateDown
	// Only used for instances that were idle and timed out waiting
	InstanceStateTimeout
)

type Function struct {
	// TODO: separate into ordered lists for more efficient lookups
	Instances map[InstanceState]map[InstanceID]Instance
	sync.RWMutex
}

// Instance represents the state of a single function instance
type Instance struct {
	InstanceID InstanceID
	IsActive   bool
	LastWorked time.Time
	Created    time.Time
}

type Worker struct {
	Functions map[FunctionID]*Function
	sync.RWMutex
}

type Workers struct {
	workers map[WorkerID]*Worker
	sync.RWMutex
	logger *slog.Logger
}

func NewWorkers(logger *slog.Logger) *Workers {
	return &Workers{
		workers: make(map[WorkerID]*Worker),
		logger:  logger,
	}
}

func (w *Workers) CreateWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	w.workers[workerID] = &Worker{
		Functions: make(map[FunctionID]*Function),
	}
}

func (w *Workers) DeleteWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	delete(w.workers, workerID)
}

func (w *Workers) GetWorker(workerID WorkerID) (*Worker, error) {
	w.RLock()
	worker, ok := w.workers[workerID]
	w.RUnlock()

	if !ok {
		return nil, fmt.Errorf("worker not found: %v", workerID)
	}
	return worker, nil
}

func (w *Workers) CreateFunction(workerID WorkerID, functionID FunctionID) error {
	worker, err := w.GetWorker(workerID)
	if err != nil {
		return err
	}

	worker.Lock()
	defer worker.Unlock()
	worker.Functions[functionID] = &Function{
		Instances: make(map[InstanceState]map[InstanceID]Instance),
	}
	return nil
}

func (w *Workers) GetFunction(workerID WorkerID, functionID FunctionID) (*Function, error) {
	worker, err := w.GetWorker(workerID)
	if err != nil {
		return nil, fmt.Errorf("error getting worker for function %v: %v", functionID, err)
	}

	worker.RLock()
	function, ok := worker.Functions[functionID]
	worker.RUnlock()

	if !ok {
		return nil, fmt.Errorf("function not found: %v", functionID)
	}
	return function, nil
}

// addInstance adds an instance to a function's state
func (f *Function) addInstance(state InstanceState, instance Instance) {
	f.Lock()
	defer f.Unlock()

	if f.Instances[state] == nil {
		f.Instances[state] = make(map[InstanceID]Instance)
	}
	f.Instances[state][instance.InstanceID] = instance
}

func (f *Function) RemoveInstance(state InstanceState, instanceID InstanceID) {
	f.Lock()
	defer f.Unlock()
	delete(f.Instances[state], instanceID)
}

// moveInstance moves an instance from one map to another. It also checks if the instance is now idle and updates its last worked time.
func (f *Function) moveInstance(fromState, toState InstanceState, instance Instance) error {
	f.Lock()
	defer f.Unlock()

	_, ok := f.Instances[fromState][instance.InstanceID]
	if !ok {
		return fmt.Errorf("instance not found in state %v: %v", fromState, instance.InstanceID)
	}

	delete(f.Instances[fromState], instance.InstanceID)

	if f.Instances[toState] == nil {
		f.Instances[toState] = make(map[InstanceID]Instance)
	}
	f.Instances[toState][instance.InstanceID] = instance
	return nil
}

func (w *Workers) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	switch instanceState {
	case InstanceStateNew:
		function.addInstance(InstanceStateRunning, instance)
	case InstanceStateIdle:
		if err := function.moveInstance(InstanceStateRunning, InstanceStateIdle, instance); err != nil {
			return err
		}
	case InstanceStateRunning:
		if err := function.moveInstance(InstanceStateIdle, InstanceStateRunning, instance); err != nil {
			return err
		}
	case InstanceStateTimeout:
		// erase from idle
		if err := w.DeleteInstance(workerID, functionID, InstanceStateIdle, instance.InstanceID); err != nil {
			return err
		}
	case InstanceStateDown:
		// erase from running
		if err := w.DeleteInstance(workerID, functionID, InstanceStateRunning, instance.InstanceID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid instance state: %v", instanceState)
	}

	w.logger.Debug("Updated instance", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance)
	return nil
}

func (w *Workers) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	function.RemoveInstance(instanceState, instanceID)
	return nil
}

func (w *Workers) FindIdleInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
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
		w.logger.Info("Found Worker with function", "id", workerID, "functionID", functionID)

		// MRU Algorithm
		function.RLock()
		lastUsed := time.Unix(0, 0)
		var instanceID InstanceID
		for id, inst := range function.Instances[InstanceStateIdle] {
			w.logger.Debug("Found idle instance", "id", id, "lastUsed", inst.LastWorked)
			if inst.LastWorked.After(lastUsed) {
				lastUsed = inst.LastWorked
				instanceID = id
			}
		}
		function.RUnlock()

		if instanceID != "" {
			// Update instance state to running
			err := w.UpdateInstance(workerID, functionID, InstanceStateRunning, function.Instances[InstanceStateIdle][instanceID])
			if err != nil {
				return "", "", err
			}
			return workerID, instanceID, nil
		}
	}

	if !functionExists {
		// TODO pick worker based on load to create function
		return "", "", &FunctionNotRegisteredError{FunctionID: functionID}
	}

	return "", "", &NoIdleInstanceError{FunctionID: functionID}
}

func (w *Workers) DebugPrint() {
	w.RLock()
	defer w.RUnlock()

	for workerID, worker := range w.workers {
		fmt.Printf("Worker ID: %v\n", workerID)

		worker.RLock()
		functions := make(map[FunctionID]*Function)
		for fID, f := range worker.Functions {
			functions[fID] = f
		}
		worker.RUnlock()

		for functionID, function := range functions {
			fmt.Printf("  Function ID: %v\n", functionID)

			function.RLock()
			instances := make(map[InstanceState]map[InstanceID]Instance)
			for state, stateInstances := range function.Instances {
				instances[state] = make(map[InstanceID]Instance)
				for id, inst := range stateInstances {
					instances[state][id] = inst
				}
			}
			function.RUnlock()

			for state, stateInstances := range instances {
				var stateStr string
				switch state {
				case InstanceStateRunning:
					stateStr = "Running"
				case InstanceStateIdle:
					stateStr = "Idle"
				default:
					stateStr = "New"
				}
				fmt.Printf("    Instance State: %v\n", stateStr)
				for _, instance := range stateInstances {
					fmt.Printf("      Instance:\n")
					fmt.Printf("        ID: %+v\n", instance.InstanceID)
					fmt.Printf("        Created: %s\n", instance.Created.Format("2006-01-02 15:04:05"))
					fmt.Printf("        LastWorked: %s\n", instance.LastWorked.Format("2006-01-02 15:04:05"))
				}
			}
		}
	}
}
