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

type FunctionStateMap struct {
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

type WorkerStateMap struct {
	Functions map[FunctionID]*FunctionStateMap
	sync.RWMutex
}

type Workers struct {
	workers map[WorkerID]*WorkerStateMap
	sync.RWMutex
	logger *slog.Logger
}

func NewWorkers(logger *slog.Logger) *Workers {
	return &Workers{
		workers: make(map[WorkerID]*WorkerStateMap),
		logger:  logger,
	}
}

// CreateWorker creates a new worker in the workers map
func (w *Workers) CreateWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	w.workers[workerID] = &WorkerStateMap{
		Functions: make(map[FunctionID]*FunctionStateMap),
	}
}

// DeleteWorker deletes a worker from the workers map
func (w *Workers) DeleteWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	delete(w.workers, workerID)
}

// GetWorker gets a worker from the workers map
func (w *Workers) GetWorker(workerID WorkerID) *WorkerStateMap {
	w.RLock()
	worker, ok := w.workers[workerID]
	w.RUnlock()

	if !ok {
		return nil
	}
	return worker
}

// AssignFunction assigns a function to a worker by saving the function in the supplied worker's map (note difference between workersMap and worker's map)
func (w *Workers) AssignFunction(workerID WorkerID, functionID FunctionID) {
	functionMap := w.GetWorker(workerID)
	if functionMap == nil {
		return
	}

	functionMap.Lock()
	defer functionMap.Unlock()
	functionMap.Functions[functionID] = &FunctionStateMap{
		Instances: make(map[InstanceState]map[InstanceID]Instance),
	}
}

// GetFunctionStateMap returns the map, which maps the following Relationship for the supplied worker: InstanceState -> map[InstanceID][]Instances
func (w *Workers) GetFunctionStateMap(workerID WorkerID, functionID FunctionID) (*FunctionStateMap, error) {
	worker := w.GetWorker(workerID)
	if worker == nil {
		return nil, fmt.Errorf("error getting function (%v) from worker, no such worker exists %v", functionID, workerID)
	}

	worker.RLock()
	function, ok := worker.Functions[functionID]
	worker.RUnlock()

	if !ok {
		return nil, fmt.Errorf("function not found: %v", functionID)
	}
	return function, nil
}

/*
UpdateInstance changes the state (which we supply) of the supplied instance (on the given worker, for the given function) according to the following rules: <Supplied> -> <New>
- InstanceStateNew -> InstanceStateRunning
- InstanceStateIdle -> InstanceStateRunning
- InstanceStateRunning -> InstanceStateIdle
For InstanceStateTimeout and InstanceStateDown delete the given instance
*/
func (w *Workers) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) error {
	function, err := w.GetFunctionStateMap(workerID, functionID)
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

// DeleteInstance deletes the supplied instance of the supplied function in th supplied state from the supplied worker
func (w *Workers) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) error {
	instanceStateMap, err := w.GetFunctionStateMap(workerID, functionID)
	if err != nil {
		return err
	}

	instanceStateMap.Lock()
	defer instanceStateMap.Unlock()
	delete(instanceStateMap.Instances[instanceState], instanceID)

	return nil
}

// FindIdleInstance looks for any idle instance for the supplied functionID
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
		w.logger.Info("Found WorkerStateMap with function", "id", workerID, "functionID", functionID)

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
		return "", "", &FunctionNotAssignedError{FunctionID: functionID}
	}

	return "", "", &NoIdleInstanceError{FunctionID: functionID}
}

func (w *Workers) DebugPrint() {
	w.RLock()
	defer w.RUnlock()

	for workerID, worker := range w.workers {
		fmt.Printf("WorkerStateMap ID: %v\n", workerID)

		worker.RLock()
		functions := make(map[FunctionID]*FunctionStateMap)
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

// addInstance adds an instance to a function's state
func (f *FunctionStateMap) addInstance(state InstanceState, instance Instance) {
	f.Lock()
	defer f.Unlock()

	if f.Instances[state] == nil {
		f.Instances[state] = make(map[InstanceID]Instance)
	}
	f.Instances[state][instance.InstanceID] = instance
}

// moveInstance moves an instance from one map to another. It also checks if the instance is now idle and updates its last worked time.
func (f *FunctionStateMap) moveInstance(fromState, toState InstanceState, instance Instance) error {
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
