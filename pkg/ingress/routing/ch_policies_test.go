package routing

import (
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
)

func TestRandomJumpKeepsHomeAndAvoidsSuccessorOverflow(t *testing.T) {
	ids := []uint64{1, 2, 3, 4, 5}
	ring := chbl.NewRing(ids)
	var fn, home, jump uint64
	for candidate := uint64(1); candidate < 10000; candidate++ {
		h := ring.Home(candidate)
		j := ids[hashFunctionID(candidate^0x9e3779b97f4a7c15)%uint64(len(ids))]
		if j != h && j != ring.Next(h) {
			fn, home, jump = candidate, h, j
			break
		}
	}
	if fn == 0 {
		t.Fatal("could not find a non-successor jump")
	}
	m := modelFor(t, randomJumpCfg(1, 1), ids, healthy(ids...))
	p := m.Picker()
	if target, err := p.Pick(RouteRequest{FunctionID: fn}); err != nil || target.LeafID != home || target.Reason != ReasonRJCHHome {
		t.Fatalf("home target=%+v err=%v", target, err)
	}
	if result := m.Apply(LeafState{LeafID: home, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: 2}); result != ChangedInPlace {
		t.Fatalf("load update result=%v, want ChangedInPlace", result)
	}
	if target, err := p.Pick(RouteRequest{FunctionID: fn}); err != nil || target.LeafID != jump || target.Reason != ReasonRJCHJump {
		t.Fatalf("jump target=%+v err=%v, want leaf %d", target, err, jump)
	}
	m.Apply(LeafState{LeafID: jump, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: 2})
	if target, err := p.Pick(RouteRequest{FunctionID: fn}); err != nil || target.Reason != ReasonRJCHLeastLoaded {
		t.Fatalf("fallback target=%+v err=%v", target, err)
	}
}

func TestCHRLUColdWarmBoundAndPerFunctionOverride(t *testing.T) {
	ids := []uint64{1, 2, 3}
	states := healthy(ids...)
	for i := range states {
		states[i].LeafLoad = 1.5
	}
	cfg := chrluCfg(1, 3)
	p := cfg.GetChRlu()
	p.MaxBound = 2
	p.HeartbeatSeconds = 0.25
	p.LatencyEstimates = []*core.FunctionLatencyEstimate{{FunctionId: 7, ColdTimeMs: 3, WarmTimeMs: 1}}
	picker := modelFor(t, cfg, ids, states).Picker().(*chrluPicker)
	if picker.heartbeatSeconds != 0.25 {
		t.Fatalf("heartbeat=%v, want 0.25", picker.heartbeatSeconds)
	}
	if got := picker.boundFor(7); got != 2 {
		t.Fatalf("weighted bound=%v, want capped 2", got)
	}
	if got := picker.boundFor(8); got != 1 {
		t.Fatalf("unknown function bound=%v, want base bound 1", got)
	}
	if target, err := picker.Pick(RouteRequest{FunctionID: 7}); err != nil || target.Reason != ReasonCHRLUHome {
		t.Fatalf("weighted target=%+v err=%v", target, err)
	}
	if target, err := picker.Pick(RouteRequest{FunctionID: 8}); err != nil || target.Reason != ReasonCHRLULeastLoaded {
		t.Fatalf("unweighted target=%+v err=%v", target, err)
	}
}

func TestCHRLUPopularFunctionUsesNoisyThreshold(t *testing.T) {
	ids := []uint64{1, 2, 3}
	states := healthy(ids...)
	for i := range states {
		states[i].LeafLoad = 0.2
	}
	cfg := chrluCfg(1, 3)
	cfg.GetChRlu().SamplePercent = 100
	p := modelFor(t, cfg, ids, states).Picker()
	const fn = uint64(123)
	first, err := p.Pick(RouteRequest{FunctionID: fn})
	if err != nil || first.Reason != ReasonCHRLUHome {
		t.Fatalf("first target=%+v err=%v", first, err)
	}
	second, err := p.Pick(RouteRequest{FunctionID: fn})
	if err != nil || second.Reason != ReasonCHRLULeastLoaded {
		t.Fatalf("popular target=%+v err=%v", second, err)
	}
}

func TestNewPoliciesPublishOnlyOnMembershipChanges(t *testing.T) {
	for name, cfg := range map[string]*core.RoutingPolicyConfig{
		"rj-ch": randomJumpCfg(1, 3), "ch-rlu": chrluCfg(1, 3),
	} {
		t.Run(name, func(t *testing.T) {
			policy, err := newRoutingPolicy(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m := policy.NewModel(testTopology(1, 2))
			if got := m.ReplaceLeaf(LeafState{LeafID: 1, FullSnapshot: true, HealthyWorkers: 1}); got != PublishPicker {
				t.Fatalf("healthy result=%v", got)
			}
			if got := m.Apply(LeafState{LeafID: 1, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: 0.5}); got != ChangedInPlace {
				t.Fatalf("load result=%v", got)
			}
			if got := m.Apply(LeafState{LeafID: 1, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: 0.5}); got != NoChange {
				t.Fatalf("unchanged result=%v", got)
			}
			if got := m.LeafDisconnected(1); got != PublishPicker {
				t.Fatalf("disconnect result=%v", got)
			}
			if _, err := m.Picker().Pick(RouteRequest{FunctionID: 3}); err == nil {
				t.Fatal("picker should reject a topology without healthy leaves")
			}
		})
	}
}

func TestNewPolicyPickersConcurrentWithLoadUpdates(t *testing.T) {
	for name, cfg := range map[string]*core.RoutingPolicyConfig{
		"rj-ch": randomJumpCfg(1, 3), "ch-rlu": chrluCfg(1, 3),
	} {
		t.Run(name, func(t *testing.T) {
			m := modelFor(t, cfg, []uint64{1, 2, 3}, healthy(1, 2, 3))
			p := m.Picker()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					m.Apply(LeafState{LeafID: 1, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: float64(i % 3)})
				}
			}()
			go func() {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					if _, err := p.Pick(RouteRequest{FunctionID: uint64(i % 5)}); err != nil {
						t.Errorf("Pick: %v", err)
						return
					}
				}
			}()
			wg.Wait()
		})
	}
}

func TestCHRLUSampledIATExpiresPopularity(t *testing.T) {
	cfg := chrluCfg(1, 3)
	cfg.GetChRlu().SamplePercent = 100
	p := modelFor(t, cfg, []uint64{1}, healthy(1)).Picker().(*chrluPicker)
	start := time.Unix(100, 0)
	if _, popular := p.observeIAT(1, start); popular {
		t.Fatal("first arrival is not popular")
	}
	if _, popular := p.observeIAT(1, start.Add(100*time.Millisecond)); !popular {
		t.Fatal("short IAT should be popular")
	}
	if _, popular := p.observeIAT(1, start.Add(10*time.Second)); popular {
		t.Fatal("long IAT should no longer be popular")
	}
}

func TestBoundedLoadsLeastLoadedIDMatchesMapScan(t *testing.T) {
	top := costTopology(64)
	model, _ := costBuild(boundedLoadsCfg(1.0, 3), top, costLeafStates(64, NeedLeafLoad, 0, 5.0))
	bl := model.(*boundedLoadsModel)
	for i := uint64(1); i <= 64; i++ {
		// Several equal loads exercise the lowest-ID tie break.
		bl.Apply(LeafState{LeafID: i, HealthyWorkers: 1, HasLeafLoad: true, LeafLoad: 2 + float64((i*7)%5)})
	}
	for _, down := range []uint64{0, 3, 17} {
		if down != 0 {
			bl.LeafDisconnected(down)
		}
		p := bl.Picker().(*boundedLoadsPicker)
		want := chbl.LeastLoadedBy(p.ring.IDs(), p)
		if got := p.LeastLoadedID(); got != want {
			t.Fatalf("after leaf %d down: LeastLoadedID = %d, LeastLoadedBy = %d", down, got, want)
		}
	}
}
