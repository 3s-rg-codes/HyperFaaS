package state

import (
	"fmt"
	"log/slog"
	"sync"
)

// Workers uses sync.Map for concurrent access safety
// The structure is equivalent to:
// map[WorkerID]map[FunctionID]map[InstanceState][]Instance
// but implemented as nested sync.Maps
// The tradeoff is the type safety...
type Workers struct {
	// Primary map: WorkerID -> nested map
	data   sync.Map
	logger *slog.Logger
}

// You'll need helper methods to handle the nested structure, for example:

func NewWorkers(logger *slog.Logger) *Workers {
	return &Workers{logger: logger}
}

// GetOrCreateWorker creates a worker's function map
func (w *Workers) CreateWorker(workerID WorkerID) *sync.Map {
	w.data.Store(workerID, &sync.Map{})
	return w.GetWorker(workerID)
}

func (w *Workers) DeleteWorker(workerID WorkerID) {
	w.data.Delete(workerID)
}

func (w *Workers) GetWorker(workerID WorkerID) *sync.Map {
	functionMap, ok := w.data.Load(workerID)
	if !ok {
		return nil
	}
	return functionMap.(*sync.Map)
}

func (w *Workers) GetFunction(workerID WorkerID, functionID FunctionID) *sync.Map {
	functionMap := w.GetWorker(workerID)
	if functionMap == nil {
		w.logger.Error("Function map not found", "workerID", workerID, "functionID", functionID)
		return nil
	}
	function, ok := functionMap.Load(functionID)
	if !ok {
		w.logger.Error("Function not found", "workerID", workerID, "functionID", functionID)
		return nil
	}
	return function.(*sync.Map)
}
func (w *Workers) CreateFunction(workerID WorkerID, functionID FunctionID) {
	w.GetWorker(workerID).Store(functionID, &sync.Map{})

	//create the running and idle maps.
	instanceStateMap := w.GetFunction(workerID, functionID)
	instanceStateMap.Store(InstanceStateRunning, []Instance{})
	instanceStateMap.Store(InstanceStateIdle, []Instance{})
}

func (w *Workers) GetInstances(workerID WorkerID, functionID FunctionID, instanceState InstanceState) []Instance {
	instanceStateMap := w.GetFunction(workerID, functionID)
	if instanceStateMap == nil {
		return nil
	}
	instanceStateSlice, ok := instanceStateMap.Load(instanceState)
	if !ok {
		return nil
	}
	return instanceStateSlice.([]Instance)
}

func (w *Workers) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) {
	instanceStateMap := w.GetFunction(workerID, functionID)
	if instanceStateMap == nil {
		w.logger.Error("Instance state map not found", "workerID", workerID, "functionID", functionID)
		return
	}
	w.logger.Debug("Updating instance", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance)
	// Todo maybe make this cleaner and update the timestamps.
	switch instanceState {
	case InstanceStateNew:
		instanceStateMap.Store(InstanceStateRunning, append(w.GetInstances(workerID, functionID, InstanceStateRunning), instance))
	case InstanceStateIdle:
		// Insert into Idle slice
		w.logger.Debug("Moving instance to idle", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance, "totalIdle", len(w.GetInstances(workerID, functionID, instanceState)))
		instanceStateMap.Store(instanceState, append(w.GetInstances(workerID, functionID, instanceState), instance))
		// Remove from Running slice
		runningInstances := w.GetInstances(workerID, functionID, InstanceStateRunning)
		for i, runningInstance := range runningInstances {
			if runningInstance.InstanceID == instance.InstanceID {
				runningInstances = append(runningInstances[:i], runningInstances[i+1:]...)
				break
			}
		}
		instanceStateMap.Store(InstanceStateRunning, runningInstances)
	case InstanceStateRunning:
		// Insert into Running slice
		w.logger.Debug("Moving instance to running", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance, "totalRunning", len(w.GetInstances(workerID, functionID, instanceState)))
		instanceStateMap.Store(instanceState, append(w.GetInstances(workerID, functionID, instanceState), instance))
		// Remove from Idle slice
		idleInstances := w.GetInstances(workerID, functionID, InstanceStateIdle)
		for i, idleInstance := range idleInstances {
			if idleInstance.InstanceID == instance.InstanceID {
				w.logger.Debug("Removing instance from idle", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance, "totalIdle", len(idleInstances))
				idleInstances = append(idleInstances[:i], idleInstances[i+1:]...)
				break
			}
		}
		instanceStateMap.Store(InstanceStateIdle, idleInstances)
	}
}

func (w *Workers) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) {
	instanceStateMap := w.GetFunction(workerID, functionID)
	if instanceStateMap == nil {
		return
	}
	instances := w.GetInstances(workerID, functionID, instanceState)
	if instances == nil {
		return
	}

	// remove the instance from the slice
	for i, instance := range instances {
		if instance.InstanceID == instanceID {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}
	instanceStateMap.Store(instanceState, instances)
}

// Traverses the State Map to find an available function instance.
// If one is found, it returns the workerID, instanceID and moves the instance to the running state.
// Returns empty strings and an error if none is found.
func (w *Workers) FindIdleInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
	var idleWorkerID WorkerID
	var idleInstanceID InstanceID
	found := false
	functionExists := false
	w.data.Range(func(workerID, functionMap interface{}) bool {
		funcMap := functionMap.(*sync.Map)
		function, ok := funcMap.Load(functionID)
		if !ok {
			// The function is not in the worker's map
			return true // Continue iterating
		}

		functionExists = true
		instanceStateMap := function.(*sync.Map)
		idleInstances, ok := instanceStateMap.Load(InstanceStateIdle)
		if !ok {
			// There are no idle instances for this function
			return true // Continue iterating
		}

		instances := idleInstances.([]Instance)
		if len(instances) > 0 {
			// There is an idle instance for this function
			idleWorkerID = workerID.(WorkerID)
			// For now just pick the first idle instance
			idleInstanceID = instances[0].InstanceID
			// Move the instance to the running state
			w.UpdateInstance(idleWorkerID, functionID, InstanceStateRunning, instances[0])
			found = true
			return false // Stop iteration
		}
		// There are no idle instances for this function
		return true // Continue iterating
	})

	if found {
		return idleWorkerID, idleInstanceID, nil
	}
	if !functionExists {
		return "", "", &FunctionNotRegisteredError{FunctionID: functionID}
	}
	return "", "", &NoIdleInstanceError{FunctionID: functionID}
}

func (w *Workers) DebugPrint() {
	w.data.Range(func(workerID, functionMap interface{}) bool {
		fmt.Printf("Worker ID: %v\n", workerID)
		funcMap := functionMap.(*sync.Map)

		funcMap.Range(func(functionID, instanceStateMap interface{}) bool {
			fmt.Printf("  Function ID: %v\n", functionID)
			stateMap := instanceStateMap.(*sync.Map)

			stateMap.Range(func(instanceState, instances interface{}) bool {
				fmt.Printf("    Instance State: %v\n", instanceState)
				for _, instance := range instances.([]Instance) {
					fmt.Printf("      Instance: %+v\n", instance)
				}
				return true
			})

			return true
		})

		return true
	})
}
