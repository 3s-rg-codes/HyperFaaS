package scheduler

import (
	"context"
	"math/rand/v2"
	"sync"

	"hyperfaas-ideal-arch/pkg/core"
)

// ReservationAware uses the same cold-start pressure signal as ColdStartAware.
// PlacementState overlays instance and cold-start-in-flight counts so the leaf
// sees in-progress boots.
type ReservationAware struct {
	mu                    sync.Mutex
	maxInstancesPerWorker int
	randomIndex           func(int) int
	eligibleIndexes       []int
}

func NewReservationAware(maxInstancesPerWorker int) *ReservationAware {
	return &ReservationAware{maxInstancesPerWorker: maxInstancesPerWorker, randomIndex: rand.N[int]}
}

func NewReservationAwareWithRand(maxInstancesPerWorker int, randomIndex func(int) int) *ReservationAware {
	if randomIndex == nil {
		randomIndex = rand.N[int]
	}
	return &ReservationAware{maxInstancesPerWorker: maxInstancesPerWorker, randomIndex: randomIndex}
}

func (r *ReservationAware) PickWorker(_ context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	_ = demand
	if len(workers) == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eligibleIndexes = r.eligibleIndexes[:0]
	for i, worker := range workers {
		if eligibleByInstanceCount(worker, r.maxInstancesPerWorker) {
			r.eligibleIndexes = append(r.eligibleIndexes, i)
		}
	}
	if len(r.eligibleIndexes) == 0 {
		return &core.PlacementDecision{Reason: "no worker instance capacity"}, nil
	}
	first := r.eligibleIndexes[r.randomIndex(len(r.eligibleIndexes))]
	best := first
	if len(r.eligibleIndexes) > 1 {
		second := first
		for second == first {
			second = r.eligibleIndexes[r.randomIndex(len(r.eligibleIndexes))]
		}
		if coldStartScore(workers[second], nil).less(coldStartScore(workers[first], nil)) {
			best = second
		}
	}
	return &core.PlacementDecision{WorkerId: workers[best].GetWorkerId(), Reason: "reservation-aware"}, nil
}
