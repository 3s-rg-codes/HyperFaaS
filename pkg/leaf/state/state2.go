package state

import (
	"fmt"
	"log/slog"
	"sync"
)

type Function2 struct {
	Instances map[InstanceState]map[InstanceID]Instance
	sync.RWMutex
}

type Worker struct {
	Functions map[FunctionID]*Function2
	sync.RWMutex
}

type Workers2 struct {
	workers map[WorkerID]*Worker
	sync.RWMutex
	logger *slog.Logger
}

func NewWorkers2(logger *slog.Logger) *Workers2 {
	return &Workers2{
		workers: make(map[WorkerID]*Worker),
		logger:  logger,
	}
}

func (w *Workers2) CreateWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	w.workers[workerID] = &Worker{
		Functions: make(map[FunctionID]*Function2),
	}
}

func (w *Workers2) DeleteWorker(workerID WorkerID) {
	w.Lock()
	defer w.Unlock()
	delete(w.workers, workerID)
}

func (w *Workers2) GetWorker(workerID WorkerID) (*Worker, error) {
	w.RLock()
	worker, ok := w.workers[workerID]
	w.RUnlock()

	if !ok {
		return nil, fmt.Errorf("worker not found: %v", workerID)
	}
	return worker, nil
}

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

func (w *Workers2) GetFunction(workerID WorkerID, functionID FunctionID) (*Function2, error) {
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
func (f *Function2) addInstance(state InstanceState, instance Instance) {
	f.Lock()
	defer f.Unlock()

	if f.Instances[state] == nil {
		f.Instances[state] = make(map[InstanceID]Instance)
	}
	f.Instances[state][instance.InstanceID] = instance
}

func (f *Function2) RemoveInstance(state InstanceState, instanceID InstanceID) {
	f.Lock()
	defer f.Unlock()
	delete(f.Instances[state], instanceID)
}

func (f *Function2) moveInstance(fromState, toState InstanceState, instanceID InstanceID) error {
	f.Lock()
	defer f.Unlock()

	instance, ok := f.Instances[fromState][instanceID]
	if !ok {
		return fmt.Errorf("instance not found in state %v: %v", fromState, instanceID)
	}

	delete(f.Instances[fromState], instanceID)

	if f.Instances[toState] == nil {
		f.Instances[toState] = make(map[InstanceID]Instance)
	}
	f.Instances[toState][instanceID] = instance

	return nil
}

func (w *Workers2) UpdateInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instance Instance) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	switch instanceState {
	case InstanceStateNew:
		function.addInstance(InstanceStateRunning, instance)
	case InstanceStateIdle:
		if err := function.moveInstance(InstanceStateRunning, InstanceStateIdle, instance.InstanceID); err != nil {
			return err
		}
	case InstanceStateRunning:
		if err := function.moveInstance(InstanceStateIdle, InstanceStateRunning, instance.InstanceID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid instance state: %v", instanceState)
	}

	w.logger.Debug("Updated instance", "workerID", workerID, "functionID", functionID, "instanceState", instanceState, "instance", instance)
	return nil
}

func (w *Workers2) DeleteInstance(workerID WorkerID, functionID FunctionID, instanceState InstanceState, instanceID InstanceID) error {
	function, err := w.GetFunction(workerID, functionID)
	if err != nil {
		return err
	}

	function.RemoveInstance(instanceState, instanceID)
	return nil
}

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
		w.logger.Info("Found Worker with function", "id", workerID, "functionID", functionID)

		function.Lock()
		idleInstances := make(map[InstanceID]Instance)
		for id, inst := range function.Instances[InstanceStateIdle] {
			idleInstances[id] = inst
		}
		function.Unlock()

		for instanceID, instance := range idleInstances {
			if err := w.UpdateInstance(workerID, functionID, InstanceStateRunning, instance); err != nil {
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

func (w *Workers2) DebugPrint() {
	w.RLock()
	defer w.RUnlock()

	for workerID, worker := range w.workers {
		fmt.Printf("Worker ID: %v\n", workerID)

		worker.RLock()
		functions := make(map[FunctionID]*Function2)
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
