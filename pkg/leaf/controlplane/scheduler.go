package controlplane

import (
	"log/slog"
	"sync"
)

// WorkerState is the scheduling state of a single worker.
type WorkerState struct {
	Index     int
	Instances int
	InFlight  int
}

// WorkerScheduler decides which worker to use for calls and scaling requests.
type WorkerScheduler interface {
	// PickForCall implements an algorithm that decides which worker to use for a call.
	// It receives a full copy of the worker states.
	// It should return -1 if none is found.
	PickForCall(state []WorkerState, maxConcurrency int) int
	// PickForScale implements an algorithm that decides which worker to use for a scaling request.
	// It receives a full copy of the worker states.
	// It should return -1 if none is found.
	PickForScale(state []WorkerState, maxInstancesPerWorker int) (int, bool)
}

// WorkerSchedulerFactory returns a scheduler instance for a given function.
type WorkerSchedulerFactory func(functionID string, workerCount int, logger *slog.Logger) WorkerScheduler

func defaultSchedulerFactory(_ string, workerCount int, _ *slog.Logger) WorkerScheduler {
	return newBalancedRoundRobin(workerCount)
}

// balancedRoundRobin is a simple scheduler that distributes calls and scaling requests evenly across all workers.
// not the best strat here for sure.
type balancedRoundRobin struct {
	mu          sync.Mutex
	workerCount int
	nextCall    int
	nextScale   int
}

func newBalancedRoundRobin(workerCount int) *balancedRoundRobin {
	if workerCount <= 0 {
		panic("workerCount must be greater than 0")
	}
	return &balancedRoundRobin{workerCount: workerCount}
}

func (b *balancedRoundRobin) PickForCall(state []WorkerState, maxConcurrency int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	n := len(state)
	if n == 0 {
		return -1
	}
	if b.workerCount != n {
		b.workerCount = n
		b.nextCall %= max(1, n)
	}
	for i := range n {
		idx := (b.nextCall + i) % n
		snapshot := state[idx]
		available := snapshot.Instances*maxConcurrency - snapshot.InFlight
		if available > 0 {
			b.nextCall = (idx + 1) % n
			return snapshot.Index
		}
	}
	return -1
}

func (b *balancedRoundRobin) PickForScale(state []WorkerState, maxInstancesPerWorker int) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(state)
	if n == 0 {
		return -1, false
	}
	if b.workerCount != n {
		b.workerCount = n
		b.nextScale %= max(1, n)
	}
	for i := range n {
		idx := (b.nextScale + i) % n
		snapshot := state[idx]
		if maxInstancesPerWorker <= 0 || snapshot.Instances < maxInstancesPerWorker {
			b.nextScale = (idx + 1) % n
			return snapshot.Index, true
		}
	}
	return -1, false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
