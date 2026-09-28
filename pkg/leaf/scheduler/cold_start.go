package scheduler

import (
	"context"
	"math"
	"sync"

	"hyperfaas-ideal-arch/pkg/core"
)

// ColdStartAware spreads new sandboxes to the least cold-start-loaded worker in
// the leaf snapshot. PlacementState overlays in-flight reservations, so burst
// scale-out keeps ramping aggressively without relying on a hard start-token cap.
type ColdStartAware struct {
	mu                    sync.Mutex
	next                  int
	maxInstancesPerWorker int
}

func NewColdStartAware(maxInstancesPerWorker int) *ColdStartAware {
	return &ColdStartAware{maxInstancesPerWorker: maxInstancesPerWorker}
}

func (c *ColdStartAware) PickWorker(_ context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	_ = demand
	if len(workers) == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next >= len(workers) {
		c.next = 0
	}
	best := -1
	var bestScore coldStartPlacementScore
	for i := range workers {
		idx := (c.next + i) % len(workers)
		worker := workers[idx]
		// Instance cap only — see eligibleByInstanceCount. Resource ratios still
		// inform coldStartScore below but must not reject workers during scale-out.
		if !eligibleByInstanceCount(worker, c.maxInstancesPerWorker) {
			continue
		}
		score := coldStartScore(worker, nil)
		if best < 0 || score.less(bestScore) {
			best = idx
			bestScore = score
		}
	}
	if best < 0 {
		return &core.PlacementDecision{Reason: "no worker instance capacity"}, nil
	}
	c.next = (best + 1) % len(workers)
	return &core.PlacementDecision{WorkerId: workers[best].GetWorkerId(), Reason: "cold-start-aware"}, nil
}

type coldStartPlacementScore struct {
	coldStarts uint32
	pressure   float64
	instances  uint64
}

func coldStartScore(worker *core.WorkerState, _ *core.ResourceSpec) coldStartPlacementScore {
	if worker == nil {
		return coldStartPlacementScore{pressure: math.MaxFloat64}
	}
	return coldStartPlacementScore{
		coldStarts: worker.GetColdStartsInFlight(),
		pressure:   observedPressure(worker),
		instances:  worker.GetInstances(),
	}
}

func (s coldStartPlacementScore) less(other coldStartPlacementScore) bool {
	if s.coldStarts != other.coldStarts {
		return s.coldStarts < other.coldStarts
	}
	if s.pressure != other.pressure {
		return s.pressure < other.pressure
	}
	return s.instances < other.instances
}
