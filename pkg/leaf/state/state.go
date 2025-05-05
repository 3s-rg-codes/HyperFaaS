package state

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	InstanceStateDown
	InstanceStateTimeout
)

type Instance struct {
	InstanceID InstanceID
	State      InstanceState
	LastWorked time.Time
	Created    time.Time
}

// --- Worker and Workers ---

type Worker struct {
	ID        WorkerID
	state     WorkerState
	Functions map[FunctionID]map[InstanceID]*Instance
	mutex     sync.RWMutex
}

type Workers struct {
	mu                sync.RWMutex
	Workers           map[WorkerID]*Worker
	functionToWorkers map[FunctionID]map[WorkerID]struct{}
	logger            *slog.Logger
}

func NewWorkers(logger *slog.Logger) *Workers {
	return &Workers{
		Workers:           make(map[WorkerID]*Worker),
		functionToWorkers: make(map[FunctionID]map[WorkerID]struct{}),
		logger:            logger,
	}
}

func (w *Workers) CreateWorker(workerID WorkerID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.Workers[workerID]; !exists {
		w.Workers[workerID] = &Worker{
			ID:        workerID,
			state:     WorkerStateUp,
			Functions: make(map[FunctionID]map[InstanceID]*Instance),
		}
	}
}

func (w *Workers) AssignFunction(workerID WorkerID, functionID FunctionID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	worker, exists := w.Workers[workerID]
	if !exists {
		return
	}

	worker.mutex.Lock()
	defer worker.mutex.Unlock()

	if _, ok := worker.Functions[functionID]; !ok {
		worker.Functions[functionID] = make(map[InstanceID]*Instance)
	}

	if _, ok := w.functionToWorkers[functionID]; !ok {
		w.functionToWorkers[functionID] = make(map[WorkerID]struct{})
	}
	w.functionToWorkers[functionID][workerID] = struct{}{}
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

func (w *Workers) FindIdleInstance(functionID FunctionID) (WorkerID, InstanceID, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	workers, ok := w.functionToWorkers[functionID]
	if !ok {
		return "", "", &FunctionNotAssignedError{FunctionID: functionID}
	}

	var bestInstance *Instance
	var selectedWorker WorkerID

	for wid := range workers {
		worker := w.Workers[wid]
		worker.mutex.RLock()
		instances := worker.Functions[functionID]

		for _, inst := range instances {
			if inst.State == InstanceStateIdle {
				if bestInstance == nil || inst.LastWorked.After(bestInstance.LastWorked) {
					temp := *inst
					bestInstance = &temp
					selectedWorker = wid
				}
			}
		}
		worker.mutex.RUnlock()
	}

	if bestInstance == nil {
		return "", "", &NoIdleInstanceError{FunctionID: functionID}
	}

	return selectedWorker, bestInstance.InstanceID, nil
}

func (w *Workers) UpdateInstance(workerID WorkerID, functionID FunctionID, state InstanceState, inst Instance) error {
	w.mu.RLock()
	worker, exists := w.Workers[workerID]
	w.mu.RUnlock()
	if !exists {
		return errors.New("worker not found")
	}

	worker.mutex.Lock()
	defer worker.mutex.Unlock()

	if _, ok := worker.Functions[functionID]; !ok {
		worker.Functions[functionID] = make(map[InstanceID]*Instance)
	}

	instance := inst // make a copy
	//TODO simplify InstanceStateNew
	if state == InstanceStateNew {
		state = InstanceStateRunning
	}

	instance.State = state
	worker.Functions[functionID][inst.InstanceID] = &instance

	// If the instance has timed out or is down, remove it from the worker's functions
	if state == InstanceStateTimeout || state == InstanceStateDown {
		delete(worker.Functions[functionID], inst.InstanceID)
	}

	return nil
}

func (w *Workers) DebugPrint() {
	w.mu.RLock()
	defer w.mu.RUnlock()

	fmt.Println("==== Debug Workers ====")
	for wid, worker := range w.Workers {
		fmt.Printf("Worker %s (%v):\n", wid, worker.state)
		worker.mutex.RLock()
		for fid, instances := range worker.Functions {
			fmt.Printf("  Function %s:\n", fid)
			var sorted []*Instance
			for _, inst := range instances {
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
		InstanceStateRunning: "Running",
		InstanceStateIdle:    "Idle",
		InstanceStateNew:     "New",
		InstanceStateDown:    "Down",
		InstanceStateTimeout: "Timeout",
	}[state]
}
