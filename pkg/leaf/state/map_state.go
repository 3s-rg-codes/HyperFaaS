package state

import (
	"fmt"
	"log/slog"
	"sync"
)

// WorkersSyncMap uses sync.Map for concurrent access safety
// The structure is equivalent to:
// map[WorkerID]map[FunctionID]map[InstanceState][]Instance
// but implemented as nested sync.Maps
// The tradeoff is the type safety...
type WorkersSyncMap struct {
	// Primary map: WorkerID -> nested map
	data   sync.Map
	logger *slog.Logger
}

func NewWorkersSyncMap(logger *slog.Logger) *WorkersSyncMap {
	return &WorkersSyncMap{logger: logger}
}

// CreateWorker creates a new worker in the workers map
func (w *WorkersSyncMap) CreateWorker(workerID WorkerID) {
	w.data.Store(workerID, &sync.Map{})
}

// DeleteWorker deletes a worker from the workers map
func (w *WorkersSyncMap) DeleteWorker(workerID WorkerID) {
	w.data.Delete(workerID)
}

// GetWorker gets a worker from the workers map
func (w *WorkersSyncMap) GetWorker(workerID WorkerID) *sync.Map {
	functionMap, ok := w.data.Load(workerID)
	if !ok {
		return nil
	}
	return functionMap.(*sync.Map)
}

// AssignFunction assigns a function to a worker by saving the function in the supplied worker's map (note difference between workersMap and worker's map)
func (w *WorkersSyncMap) AssignFunction(workerID WorkerID, functionID FunctionID) {
	w.GetWorker(workerID).Store(functionID, &sync.Map{})

	//create the running and idle maps.
	instanceStateMap, err := w.GetFunctionStateMap(workerID, functionID)
	if err != nil {
		w.logger.Error(err.Error())
		return
	}
	instanceStateMap.Store(InstanceStateRunning, []Instance{})
	instanceStateMap.Store(InstanceStateIdle, []Instance{})
}

// GetFunctionStateMap
func (w *WorkersSyncMap) GetFunctionStateMap(workerID WorkerID, functionID FunctionID) (*sync.Map, error) {
	functionMap := w.GetWorker(workerID)
	if functionMap == nil {
		w.logger.Error("Function map not found", "workerID", workerID, "functionID", functionID)
		return nil, fmt.Errorf("error getting function (%v) from worker, no such worker exists %v", functionID, workerID)
	}
	function, ok := functionMap.Load(functionID)
	if !ok {
		w.logger.Error("Function not found", "workerID", workerID, "functionID", functionID)
		return nil, fmt.Errorf("no such function (%v) on worker (%v)", functionID, workerID)
	}
	return function.(*sync.Map), nil
}

// GetInstances returns all instances with the supplied function state on the supplied worker
func (w *WorkersSyncMap) GetInstances(workerID WorkerID, functionID FunctionID, instanceState InstanceState) []Instance {
	instanceStateMap, err := w.GetFunctionStateMap(workerID, functionID)
	if err != nil {
		w.logger.Error(err.Error())
		return []Instance{}
	}
	if instanceStateMap == nil {
		return nil
	}
	instanceStateSlice, ok := instanceStateMap.Load(instanceState)
	if !ok {
		return nil
	}
	return instanceStateSlice.([]Instance)
}

func (w *WorkersSyncMap) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) error {
	instanceStateMap, err := w.GetFunctionStateMap(workerID, functionID)
	if err != nil {
		return err
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
	case InstanceStateTimeout:
		// Remove from Idle
		idleInstances := w.GetInstances(workerID, functionID, InstanceStateIdle)
		for i, idleInstance := range idleInstances {
			if idleInstance.InstanceID == instance.InstanceID {
				idleInstances = append(idleInstances[:i], idleInstances[i+1:]...)
				break
			}
		}
	case InstanceStateDown:
		// Remove from Running
		runningInstances := w.GetInstances(workerID, functionID, InstanceStateRunning)
		for i, runningInstance := range runningInstances {
			if runningInstance.InstanceID == instance.InstanceID {
				runningInstances = append(runningInstances[:i], runningInstances[i+1:]...)
				break
			}
		}
	}
	return nil
}

// DeleteInstance deletes the supplied instance of the supplied function in th supplied state from the supplied worker
func (w *WorkersSyncMap) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) error {
	instanceStateMap, err := w.GetFunctionStateMap(workerID, functionID)
	if err != nil {
		return err
	}
	instances := w.GetInstances(workerID, functionID, instanceState)
	if instances == nil {
		return err
	}

	// remove the instance from the slice
	for i, instance := range instances {
		if instance.InstanceID == instanceID {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}

	instanceStateMap.Store(instanceState, instances)
	return nil
}

// Traverses the State Map to find an available function instance.
// If one is found, it returns the workerID, instanceID and moves the instance to the running state.
// Returns empty strings and an error if none is found.
func (w *WorkersSyncMap) FindIdleInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
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
			// Find the instance that worked most recently
			mostRecent := instances[0]
			for _, instance := range instances[1:] {
				if instance.LastWorked.After(mostRecent.LastWorked) {
					mostRecent = instance
				}
			}
			idleInstanceID = mostRecent.InstanceID
			// Move the instance to the running state
			w.UpdateInstance(idleWorkerID, functionID, InstanceStateRunning, mostRecent)
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
		return "", "", &FunctionNotAssignedError{FunctionID: functionID}
	} //TODO would be more like FunctionNotAssigned since Assigning != Creating
	return "", "", &NoIdleInstanceError{FunctionID: functionID}
}

func (w *WorkersSyncMap) DebugPrint() {
	w.data.Range(func(workerID, functionMap interface{}) bool {
		fmt.Printf("WorkerStateMap ID: %v\n", workerID)
		funcMap := functionMap.(*sync.Map)

		funcMap.Range(func(functionID, instanceStateMap interface{}) bool {
			fmt.Printf("  Function ID: %v\n", functionID)
			stateMap := instanceStateMap.(*sync.Map)

			stateMap.Range(func(instanceState, instances interface{}) bool {
				// Instance State 0 is Running
				// Instance State 1 is Idle
				var isS string
				if instanceState == InstanceStateRunning {
					isS = "Running"
				} else if instanceState == InstanceStateIdle {
					isS = "Idle"
				} else {
					isS = "New"
				}
				fmt.Printf("    Instance State: %v\n", isS)
				for _, instance := range instances.([]Instance) {
					fmt.Printf("      Instance:\n")
					fmt.Printf("        ID: %+v\n", instance.InstanceID)
					fmt.Printf("        Created: %s\n", instance.Created.Format("2006-01-02 15:04:05"))
					fmt.Printf("        LastWorked: %s\n", instance.LastWorked.Format("2006-01-02 15:04:05"))
				}
				return true
			})

			return true
		})

		return true
	})
}
