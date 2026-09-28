package scheduler

import (
	"context"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
)

// BoundedLoads is worker-level Fuerst CH-BL (the paper replica).
// Load is WorkerState.load_average_norm. Ingress must not see this vector.
type BoundedLoads struct {
	maxInstancesPerWorker int
	bound                 float64
	maxChainLen           int
	// ring is built once from the worker ID set, which is static for the life
	// of a placement configuration. Building it per call costs O(W*ringReplicas)
	// and dominated every other placement policy.
	ring *chbl.Ring
}

func NewBoundedLoads(workerCount, maxInstancesPerWorker int, bound float64, maxChainLen int) *BoundedLoads {
	ids := make([]uint64, 0, workerCount)
	for i := 0; i < workerCount; i++ {
		ids = append(ids, uint64(i+1))
	}
	return &BoundedLoads{
		maxInstancesPerWorker: maxInstancesPerWorker,
		bound:                 chbl.NormalizeBound(bound),
		maxChainLen:           chbl.NormalizeMaxChainLen(maxChainLen),
		ring:                  chbl.NewRing(ids),
	}
}

func (s *BoundedLoads) PickWorker(_ context.Context, function *core.FunctionSpec, workers []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	// Only the ring is shared; it is immutable after construction. Per-call
	// state stays local because different function controllers call PickWorker
	// concurrently.
	byID := make(map[uint64]*core.WorkerState, len(workers))
	loads := make(map[uint64]float64, len(workers))
	for _, w := range workers {
		if w == nil || w.GetWorkerId() == 0 {
			continue
		}
		id := w.GetWorkerId()
		byID[id] = w
		if !eligibleByInstanceCount(w, s.maxInstancesPerWorker) {
			// Unschedulable workers are treated as over the bound so the ring walks past them.
			loads[id] = s.bound + 1
			continue
		}
		loads[id] = w.GetLoadAverageNorm()
	}
	if len(byID) == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	functionID := uint64(0)
	if function != nil {
		functionID = function.GetFunctionId()
	}
	home := s.ring.Home(functionID)
	id, reason := chbl.Forward(s.ring, loads, s.bound, s.maxChainLen, home)
	w := byID[id]
	if w == nil || !eligibleByInstanceCount(w, s.maxInstancesPerWorker) {
		id = leastLoadedEligible(s.ring.IDs(), loads, byID, s.maxInstancesPerWorker)
		w = byID[id]
		reason = chbl.ReasonLeastLoaded
	}
	if w == nil || !eligibleByInstanceCount(w, s.maxInstancesPerWorker) {
		return &core.PlacementDecision{Reason: "no worker capacity"}, nil
	}
	return &core.PlacementDecision{
		WorkerId: id,
		Reason:   reason,
		Score:    w.GetLoadAverageNorm(),
	}, nil
}

func leastLoadedEligible(ids []uint64, loads map[uint64]float64, byID map[uint64]*core.WorkerState, maxInstances int) uint64 {
	var best uint64
	found := false
	for _, id := range ids {
		w := byID[id]
		if !eligibleByInstanceCount(w, maxInstances) {
			continue
		}
		if !found || loads[id] < loads[best] || (loads[id] == loads[best] && id < best) {
			best = id
			found = true
		}
	}
	return best
}
