package scheduler

import (
	"context"
	"sync/atomic"

	"hyperfaas-ideal-arch/pkg/core"
)

// BalancedRoundRobin distributes placement across workers.
type BalancedRoundRobin struct {
	workerCount           int
	nextScale             atomic.Uint64
	maxInstancesPerWorker int
}

func NewBalancedRoundRobin(workerCount, maxInstancesPerWorker int) *BalancedRoundRobin {
	if workerCount <= 0 {
		panic("scheduler: workerCount must be > 0")
	}
	return &BalancedRoundRobin{
		workerCount:           workerCount,
		maxInstancesPerWorker: maxInstancesPerWorker,
	}
}

func (b *BalancedRoundRobin) PickWorker(_ context.Context, _ *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	_ = demand
	n := len(workers)
	if n == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	start := b.NextWorkerIndex(n)
	for i := range n {
		idx := (start + i) % n
		w := workers[idx]
		if eligibleByInstanceCount(w, b.maxInstancesPerWorker) {
			return &core.PlacementDecision{
				WorkerId: w.GetWorkerId(),
				Reason:   "balanced-round-robin",
			}, nil
		}
	}
	return &core.PlacementDecision{Reason: "no worker capacity"}, nil
}

// NextWorkerIndex returns a lock-free fair starting point. PlacementState uses
// this method for its atomic reservation fast path.
func (b *BalancedRoundRobin) NextWorkerIndex(workerCount int) int {
	if workerCount <= 0 {
		return 0
	}
	return int((b.nextScale.Add(1) - 1) % uint64(workerCount))
}
