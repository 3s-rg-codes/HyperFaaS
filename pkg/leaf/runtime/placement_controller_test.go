package runtime

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf/scheduler"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
)

// fakePlacementWorker records the projection each stream was opened with and
// delivers an immediate baseline, so ApplyConfig does not wait for a timeout.
type fakePlacementWorker struct {
	index int

	mu          sync.Mutex
	projections []leafworker.StateProjection
}

func (w *fakePlacementWorker) Index() int { return w.index }

func (w *fakePlacementWorker) StartWatch(_ context.Context, _ time.Duration, projection leafworker.StateProjection, onUpdate func(*core.WorkerState)) {
	w.mu.Lock()
	w.projections = append(w.projections, projection)
	w.mu.Unlock()
	if onUpdate != nil {
		onUpdate(&core.WorkerState{WorkerId: uint64(w.index + 1), Healthy: true, Schedulable: true})
	}
}

func (w *fakePlacementWorker) lastProjection() leafworker.StateProjection {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.projections) == 0 {
		return leafworker.StateProjection{}
	}
	return w.projections[len(w.projections)-1]
}

func balancedPlacementCfg() *core.PlacementPolicyConfig {
	return &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BalancedRoundRobin{BalancedRoundRobin: &core.BalancedRoundRobinPlacement{}}}
}

func imageAwarePlacementCfg() *core.PlacementPolicyConfig {
	return &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ImageAware{ImageAware: &core.ImageAwarePlacement{}}}
}

func boundedLoadsRoutingCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_BoundedLoads{BoundedLoads: &core.BoundedLoadsRoutingPolicy{Bound: 1.0, MaxChainLen: 3}}}
}

func TestLoadRoutingPoliciesRequestWorkerLoad(t *testing.T) {
	policies := map[string]*core.RoutingPolicyConfig{
		"bounded-loads": boundedLoadsRoutingCfg(),
		"rj-ch":         {Policy: &core.RoutingPolicyConfig_RjCh{RjCh: &core.RandomJumpRoutingPolicy{}}},
		"ch-rlu":        {Policy: &core.RoutingPolicyConfig_ChRlu{ChRlu: &core.CHRLURoutingPolicy{}}},
	}
	for name, routing := range policies {
		t.Run(name, func(t *testing.T) {
			ctl, _, workers := newTestPlacementController(t)
			if err := ctl.ApplyConfig(context.Background(), balancedPlacementCfg(), routing, 1); err != nil {
				t.Fatal(err)
			}
			for _, worker := range workers {
				if !worker.lastProjection().LoadAverageNorm {
					t.Fatal("worker load was omitted from the projection")
				}
			}
		})
	}
}

func newTestPlacementController(t *testing.T) (*PlacementController, *PlacementState, []*fakePlacementWorker) {
	t.Helper()
	workers := []*fakePlacementWorker{{index: 0}, {index: 1}}
	placement := NewPlacementState(len(workers), 0, 0, 0)
	ctl := NewPlacementController(
		[]placementWorker{workers[0], workers[1]},
		placement,
		time.Millisecond,
		0,
		100*time.Millisecond,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		nil,
	)
	return ctl, placement, workers
}

func TestPlacementControllerUnavailableBeforeFirstPolicy(t *testing.T) {
	ctl, placement, _ := newTestPlacementController(t)
	if ctl.Active() != nil {
		t.Fatal("no scheduler should be active before the first policy")
	}
	if _, err := ctl.PickWorker(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("expected placement to be unavailable before the first policy")
	}
	choice, _, _, _, err := placement.Schedule(context.Background(), ctl, nil, nil, nil)
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	if choice.WorkerID != 0 {
		t.Fatalf("placement should not pick a worker while stopped, got %d", choice.WorkerID)
	}
}

func TestPlacementControllerAppliesAndReloadsPolicy(t *testing.T) {
	ctl, placement, workers := newTestPlacementController(t)
	ctx := context.Background()

	if err := ctl.ApplyConfig(ctx, balancedPlacementCfg(), nil, 1); err != nil {
		t.Fatalf("ApplyConfig(balanced): %v", err)
	}
	if _, ok := ctl.Active().(*scheduler.BalancedRoundRobin); !ok {
		t.Fatalf("active scheduler = %T, want *BalancedRoundRobin", ctl.Active())
	}
	for _, w := range workers {
		if p := w.lastProjection(); p.LoadAverageNorm || p.CachedImages {
			t.Fatalf("balanced placement projection = %+v, want no gated signals", p)
		}
	}

	// A schedule through the controller facade reaches the active scheduler.
	_, reservation, _, _, err := placement.Schedule(ctx, ctl, nil, nil, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("Schedule reservation=%v error=%v", reservation, err)
	}
	placement.CancelReservation(reservation)

	if err := ctl.ApplyConfig(ctx, imageAwarePlacementCfg(), nil, 2); err != nil {
		t.Fatalf("ApplyConfig(image-aware): %v", err)
	}
	if _, ok := ctl.Active().(*scheduler.ImageAware); !ok {
		t.Fatalf("active scheduler = %T, want *ImageAware", ctl.Active())
	}
	for _, w := range workers {
		if p := w.lastProjection(); !p.CachedImages {
			t.Fatalf("image-aware projection = %+v, want cached_images", p)
		}
	}
	if !placement.trackImages.Load() {
		t.Fatal("image-aware placement must enable leaf-local image tracking")
	}

	// Bounded-loads ingress routing needs the leaf's leaf_load scalar, which is
	// max(worker.load_average_norm), so the worker projection must request load
	// even when placement itself does not.
	if err := ctl.ApplyConfig(ctx, balancedPlacementCfg(), boundedLoadsRoutingCfg(), 3); err != nil {
		t.Fatalf("ApplyConfig(balanced, bounded-loads routing): %v", err)
	}
	for _, w := range workers {
		if p := w.lastProjection(); !p.LoadAverageNorm {
			t.Fatalf("projection = %+v, want load_average_norm for bounded-loads routing", p)
		}
	}
	if placement.trackImages.Load() {
		t.Fatal("balanced placement must disable leaf-local image tracking")
	}
}
