package state

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
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
	InstanceStateStarting
	InstanceStateDown
	InstanceStateTimeout
)

type Instance struct {
	InstanceID       InstanceID
	State            InstanceState
	MaxConcurrency   int32
	ConcurrencyLevel atomic.Int32
	LastWorked       time.Time
	Created          time.Time
}

// --- Worker and Workers ---
type Function struct {
	FunctionID               FunctionID
	MaxConcurrency           int32
	Instances                map[InstanceID]*Instance
	CurrentStartingInstances atomic.Int32
	ConcurrencyLevel         atomic.Int32
	mutex                    sync.RWMutex
}

type Worker struct {
	ID        WorkerID
	state     WorkerState
	Functions map[FunctionID]*Function
	mutex     sync.RWMutex
}

type Workers struct {
	mu                   sync.RWMutex
	Workers              map[WorkerID]*Worker
	functionToWorkers    map[FunctionID]map[WorkerID]struct{}
	logger               *slog.Logger
	maxStartingInstances int32
}

func NewWorkers(logger *slog.Logger, maxStartingInstances int32) *Workers {
	return &Workers{
		Workers:              make(map[WorkerID]*Worker),
		functionToWorkers:    make(map[FunctionID]map[WorkerID]struct{}),
		logger:               logger,
		maxStartingInstances: maxStartingInstances,
	}
}

func (w *Workers) CreateWorker(workerID WorkerID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.Workers[workerID]; !exists {
		w.Workers[workerID] = &Worker{
			ID:        workerID,
			state:     WorkerStateUp,
			Functions: make(map[FunctionID]*Function),
		}
	}
}

func (w *Workers) AssignFunction(workerID WorkerID, functionID FunctionID, maxConcurrency int32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	worker, exists := w.Workers[workerID]
	if !exists {
		return &WorkerNotFoundError{WorkerID: workerID}
	}

	worker.mutex.Lock()
	defer worker.mutex.Unlock()

	_, exists = worker.Functions[functionID]
	if exists {
		return &FunctionAlreadyAssignedError{FunctionID: functionID}
	} else {
		worker.Functions[functionID] = &Function{
			FunctionID:     functionID,
			MaxConcurrency: maxConcurrency,
			Instances:      make(map[InstanceID]*Instance),
		}
	}

	if _, ok := w.functionToWorkers[functionID]; !ok {
		w.functionToWorkers[functionID] = make(map[WorkerID]struct{})
	}
	w.functionToWorkers[functionID][workerID] = struct{}{}
	return nil
}

func (w *Workers) DeleteWorker(workerID WorkerID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	worker, exists := w.Workers[workerID]
	if !exists {
		return
	}

	worker.mutex.Lock()
	defer worker.mutex.Unlock()

	for functionID := range worker.Functions {
		delete(w.functionToWorkers[functionID], workerID)
		if len(w.functionToWorkers[functionID]) == 0 {
			delete(w.functionToWorkers, functionID)
		}
	}
	delete(w.Workers, workerID)
}

func (w *Workers) FindMRUInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
	w.mu.RLock()
	workers, ok := w.functionToWorkers[functionID]
	w.mu.RUnlock()
	if !ok {
		return "", "", &FunctionNotAssignedError{FunctionID: functionID}
	}

	var bestInstance *Instance
	var selectedWorker WorkerID
	var functionCurrentStartingInstances int32
	for wid := range workers {
		worker := w.Workers[wid]
		worker.mutex.RLock()
		function := worker.Functions[functionID]
		worker.mutex.RUnlock()
		function.mutex.RLock()
		for _, inst := range function.Instances {
			if inst.ConcurrencyLevel.Load() >= function.MaxConcurrency {
				w.logger.Debug("Instance concurrency level exceeds max concurrency", "instanceID", inst.InstanceID, "workerID", wid, "concurrencyLevel", inst.ConcurrencyLevel.Load(), "maxConcurrency", function.MaxConcurrency)
				continue
			}

			if bestInstance == nil || inst.LastWorked.After(bestInstance.LastWorked) {
				bestInstance = inst
				selectedWorker = wid
			}
		}
		if bestInstance != nil {
			w.logger.Debug("Selected instance", "instanceID", bestInstance.InstanceID, "workerID", selectedWorker, "concurrencyLevel", bestInstance.ConcurrencyLevel.Load())
			bestInstance.ConcurrencyLevel.Add(1)
			bestInstance.LastWorked = time.Now()
		}
		functionCurrentStartingInstances = function.CurrentStartingInstances.Load()
		function.mutex.RUnlock()
	}

	if bestInstance == nil && functionCurrentStartingInstances < w.maxStartingInstances {
		return "", "", &NoIdleInstanceError{FunctionID: functionID}
	} else if bestInstance == nil && functionCurrentStartingInstances >= w.maxStartingInstances {
		return "", "", &TooManyStartingInstancesError{FunctionID: functionID}
	}

	return selectedWorker, bestInstance.InstanceID, nil
}

func (w *Workers) UpdateStartingInstancesCounter(workerID WorkerID, functionID FunctionID, delta int32) {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return
	}

	worker.mutex.RLock()
	function, exists := worker.Functions[functionID]
	worker.mutex.RUnlock()
	if !exists {
		return
	}

	function.mutex.RLock()
	function.CurrentStartingInstances.Add(delta)
	function.mutex.RUnlock()
}

func (w *Workers) ReduceInstanceConcurrency(workerID WorkerID, functionID FunctionID, instanceID InstanceID) {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return
	}

	worker.mutex.RLock()
	function, exists := worker.Functions[functionID]
	worker.mutex.RUnlock()
	if !exists {
		return
	}

	function.mutex.RLock()
	instance, exists := function.Instances[instanceID]
	function.mutex.RUnlock()
	if !exists {
		return
	}
	instance.ConcurrencyLevel.Add(-1)
}

func (w *Workers) CountInstancesInState(workerID WorkerID, functionID FunctionID, instanceState InstanceState) (int, error) {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return 0, &WorkerNotFoundError{WorkerID: workerID}
	}

	worker.mutex.RLock()
	defer worker.mutex.RUnlock()

	function, ok := worker.Functions[functionID]
	if !ok {
		return 0, &FunctionNotAssignedError{FunctionID: functionID}
	}

	count := 0
	for _, inst := range function.Instances {
		if inst.State == instanceState {
			count++
		}
	}

	return count, nil
}

func (w *Workers) AddInstance(workerID WorkerID, functionID FunctionID, instanceID InstanceID) error {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return &WorkerNotFoundError{WorkerID: workerID}
	}

	worker.mutex.RLock()
	function, exists := worker.Functions[functionID]
	worker.mutex.RUnlock()
	if !exists {
		return &FunctionNotAssignedError{FunctionID: functionID}
	}

	function.mutex.Lock()
	function.Instances[instanceID] = &Instance{
		InstanceID:       instanceID,
		State:            InstanceStateIdle,
		MaxConcurrency:   function.MaxConcurrency,
		ConcurrencyLevel: atomic.Int32{},
		LastWorked:       time.Now(),
		Created:          time.Now(),
	}
	function.mutex.Unlock()

	return nil
}

func (w *Workers) RemoveInstance(workerID WorkerID, functionID FunctionID, instanceID InstanceID) error {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return &WorkerNotFoundError{WorkerID: workerID}
	}

	worker.mutex.RLock()
	function, exists := worker.Functions[functionID]
	worker.mutex.RUnlock()
	if !exists {
		return &FunctionNotAssignedError{FunctionID: functionID}
	}

	function.mutex.RLock()
	_, exists = function.Instances[instanceID]
	function.mutex.RUnlock()
	if !exists {
		return &InstanceNotFoundError{InstanceID: instanceID}
	}
	function.mutex.Lock()
	delete(function.Instances, instanceID)
	function.mutex.Unlock()

	return nil
}

func (w *Workers) DebugPrint() {
	w.mu.RLock()
	defer w.mu.RUnlock()

	fmt.Println("==== Debug Workers ====")
	for wid, worker := range w.Workers {
		fmt.Printf("Worker %s (%v):\n", wid, worker.state)
		worker.mutex.RLock()
		for fid, function := range worker.Functions {
			fmt.Printf("  Function %s:\n", fid)
			var sorted []*Instance
			for _, inst := range function.Instances {
				sorted = append(sorted, inst)
			}
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].LastWorked.After(sorted[j].LastWorked)
			})
			for _, inst := range sorted {
				fmt.Printf("    - %s State: %s LastWorked: %v\n", inst.InstanceID, InstanceStateToString(inst.State), inst.LastWorked)
			}
		}
		worker.mutex.RUnlock()
	}
	fmt.Println("=======================")
}

func InstanceStateToString(state InstanceState) string {
	return []string{
		InstanceStateRunning:  "Running",
		InstanceStateIdle:     "Idle",
		InstanceStateStarting: "Starting",
		InstanceStateDown:     "Down",
		InstanceStateTimeout:  "Timeout",
	}[state]
}
