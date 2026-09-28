package scheduler

import (
	"context"
	"sync"

	"hyperfaas-ideal-arch/pkg/core"
)

// ImageAware prefers workers that already advertise the function image in
// WorkerState.cached_images. Among hits (and among misses) it uses the cold-start
// score so affinity does not ignore concurrent start pressure.
type ImageAware struct {
	mu                    sync.Mutex
	next                  int
	maxInstancesPerWorker int
}

func NewImageAware(maxInstancesPerWorker int) *ImageAware {
	return &ImageAware{maxInstancesPerWorker: maxInstancesPerWorker}
}

func (s *ImageAware) PickWorker(_ context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	_ = demand
	if len(workers) == 0 {
		return &core.PlacementDecision{Reason: "no workers"}, nil
	}
	image := FunctionImageRef(function)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(workers) {
		s.next = 0
	}

	bestHit, bestMiss := -1, -1
	var hitScore, missScore coldStartPlacementScore
	for i := range workers {
		idx := (s.next + i) % len(workers)
		worker := workers[idx]
		if !eligibleByInstanceCount(worker, s.maxInstancesPerWorker) {
			continue
		}
		score := coldStartScore(worker, nil)
		if image != "" && WorkerHasCachedImage(worker, image) {
			if bestHit < 0 || score.less(hitScore) {
				bestHit = idx
				hitScore = score
			}
			continue
		}
		if bestMiss < 0 || score.less(missScore) {
			bestMiss = idx
			missScore = score
		}
	}
	best := bestHit
	reason := "image-aware-hit"
	if best < 0 {
		best = bestMiss
		reason = "image-aware-miss"
	}
	if best < 0 {
		return &core.PlacementDecision{Reason: "no worker instance capacity"}, nil
	}
	s.next = (best + 1) % len(workers)
	return &core.PlacementDecision{WorkerId: workers[best].GetWorkerId(), Reason: reason}, nil
}
