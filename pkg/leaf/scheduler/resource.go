package scheduler

import (
	"context"
	"math"
	"sync"

	"hyperfaas-ideal-arch/pkg/core"
)

// ResourceAware spreads sandboxes toward workers with the lowest observed host
// pressure (CPU/memory/disk telemetry). It does not reject placement based on
// declared function resource requests; hardware limits surface via worker health.
type ResourceAware struct {
	mu                    sync.Mutex
	next                  int
	maxInstancesPerWorker int
}

func NewResourceAware(maxInstancesPerWorker int) *ResourceAware {
	return &ResourceAware{maxInstancesPerWorker: maxInstancesPerWorker}
}

func (r *ResourceAware) PickWorker(_ context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	_ = demand
	if len(workers) == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	best := -1
	bestScore := math.MaxFloat64
	for i := range workers {
		// Start each scan at the cursor so equal-score workers rotate instead of
		// always biasing the lowest configured index.
		idx := (r.next + i) % len(workers)
		w := workers[idx]
		if !eligibleByInstanceCount(w, r.maxInstancesPerWorker) {
			continue
		}
		// Prefer workers with the lowest observed host pressure so sandboxes spread
		// across hardware without rejecting on declared function resource requests.
		score := observedPressure(w)
		if score < bestScore {
			best = idx
			bestScore = score
		}
	}
	if best < 0 {
		return &core.PlacementDecision{Reason: "no worker resource capacity"}, nil
	}
	r.next = (best + 1) % len(workers)
	return &core.PlacementDecision{WorkerId: workers[best].GetWorkerId(), Reason: "resource-aware"}, nil
}

func requestedResources(function *core.FunctionSpec) *core.ResourceSpec {
	if function == nil || function.GetRuntime() == nil || function.GetRuntime().GetResources() == nil {
		return &core.ResourceSpec{}
	}
	return function.GetRuntime().GetResources()
}

// eligibleByInstanceCount gates placement on worker health and the per-worker
// instance cap only. Schedulers use this instead of declared-resource fits so scale-out is
// not hard-capped by declared function CPU/memory (e.g. budget_cpu=2 with
// cpu_units=100 per sandbox → 20 instances/worker) before real hardware limits.
// Dirigent's random placement ignores declared resources the same way.
// maxInstances <= 0 means unlimited (no per-worker instance cap).
func eligibleByInstanceCount(w *core.WorkerState, maxInstances int) bool {
	if w == nil || w.GetWorkerId() == 0 || !w.GetHealthy() || !w.GetSchedulable() {
		return false
	}
	if maxInstances <= 0 {
		return true
	}
	return int(w.GetInstances()) < maxInstances
}

// observedPressure scores a worker from host telemetry only (no declared function
// resources). Used to spread sandboxes toward less-loaded hardware.
func observedPressure(w *core.WorkerState) float64 {
	cap := w.GetCapacity()
	used := w.GetAllocated()
	if cap == nil || used == nil {
		return float64(w.GetInstances())
	}
	return maxRatio(ratio(used.GetCpuUnits(), cap.GetCpuUnits()),
		ratio(used.GetMemoryBytes(), cap.GetMemoryBytes()),
		ratio(used.GetDiskBytes(), cap.GetDiskBytes()))
}

func ratio(used, capacity uint64) float64 {
	if capacity == 0 {
		return 0
	}
	return float64(used) / float64(capacity)
}

func maxRatio(a, b, c float64) float64 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}
