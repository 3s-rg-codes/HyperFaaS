package routing

import (
	"fmt"
	"sync"
	"testing"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
)

func randomCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_Random{Random: &core.RandomRoutingPolicy{}}}
}
func roundRobinCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_RoundRobin{RoundRobin: &core.RoundRobinRoutingPolicy{}}}
}
func consistentCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_ConsistentHashing{ConsistentHashing: &core.ConsistentHashingRoutingPolicy{}}}
}
func leastLoadedCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_LeastLoaded{LeastLoaded: &core.LeastLoadedRoutingPolicy{}}}
}
func boundedLoadsCfg(bound float64, maxChain int) *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_BoundedLoads{BoundedLoads: &core.BoundedLoadsRoutingPolicy{Bound: bound, MaxChainLen: int32(maxChain)}}}
}
func randomJumpCfg(bound float64, maxJumps int) *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_RjCh{RjCh: &core.RandomJumpRoutingPolicy{Bound: bound, MaxJumps: int32(maxJumps)}}}
}
func chrluCfg(bound float64, maxChain int) *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_ChRlu{ChRlu: &core.CHRLURoutingPolicy{Bound: bound, MaxChainLen: int32(maxChain)}}}
}
func availableCapacityCfg() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_AvailableCapacity{AvailableCapacity: &core.AvailableCapacityRoutingPolicy{}}}
}

func testTopology(ids ...uint64) Topology {
	addrs := make(map[uint64]LeafAddress, len(ids))
	for _, id := range ids {
		addrs[id] = LeafAddress{
			HTTPAddress:    fmt.Sprintf("http://leaf-%d", id),
			ControlAddress: fmt.Sprintf("leaf-%d:9000", id),
		}
	}
	return NewTopology(addrs)
}

func modelFor(t *testing.T, cfg *core.RoutingPolicyConfig, ids []uint64, states []LeafState) routingModel {
	t.Helper()
	policy, err := newRoutingPolicy(cfg)
	if err != nil {
		t.Fatalf("newRoutingPolicy: %v", err)
	}
	model := policy.NewModel(testTopology(ids...))
	for _, s := range states {
		model.ReplaceLeaf(s)
	}
	return model
}

func healthy(ids ...uint64) []LeafState {
	states := make([]LeafState, len(ids))
	for i, id := range ids {
		states[i] = LeafState{LeafID: id, FullSnapshot: true, HealthyWorkers: 1}
	}
	return states
}

func TestNeedsForEachPolicy(t *testing.T) {
	cases := []struct {
		name string
		cfg  *core.RoutingPolicyConfig
		want RoutingNeeds
	}{
		{"random", randomCfg(), 0},
		{"round-robin", roundRobinCfg(), 0},
		{"consistent-hashing", consistentCfg(), 0},
		{"least-loaded", leastLoadedCfg(), NeedAggregateInFlight},
		{"bounded-loads", boundedLoadsCfg(1, 3), NeedLeafLoad},
		{"rj-ch", randomJumpCfg(1, 3), NeedLeafLoad},
		{"ch-rlu", chrluCfg(1, 3), NeedLeafLoad},
		{"available-capacity", availableCapacityCfg(), NeedFunctionCapacity},
	}
	for _, tc := range cases {
		got, err := NeedsFor(tc.cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s needs = %b, want %b", tc.name, got, tc.want)
		}
	}
}

func TestEngineUnavailableBeforeInstall(t *testing.T) {
	e := NewEngine()
	if _, err := e.Pick(RouteRequest{FunctionID: 1}); err == nil {
		t.Fatal("expected routing to be unavailable before a policy is installed")
	}
}

func TestRoundRobinAndRandomCoverHealthyLeaves(t *testing.T) {
	ids := []uint64{1, 2, 3, 4}
	states := healthy(ids...)

	rr := modelFor(t, roundRobinCfg(), ids, states).Picker()
	seen := map[uint64]int{}
	for i := 0; i < 8; i++ {
		target, err := rr.Pick(RouteRequest{FunctionID: 7})
		if err != nil {
			t.Fatal(err)
		}
		seen[target.LeafID]++
	}
	if len(seen) != 4 {
		t.Fatalf("round-robin should cover all healthy leaves, got %v", seen)
	}

	rnd := modelFor(t, randomCfg(), ids, states).Picker()
	if _, err := rnd.Pick(RouteRequest{FunctionID: 7}); err != nil {
		t.Fatal(err)
	}
}

func TestConsistentHashingIsSticky(t *testing.T) {
	ids := []uint64{1, 2, 3, 4}
	picker := modelFor(t, consistentCfg(), ids, healthy(ids...)).Picker()
	a, err := picker.Pick(RouteRequest{FunctionID: 42})
	if err != nil {
		t.Fatal(err)
	}
	b, err := picker.Pick(RouteRequest{FunctionID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if a.LeafID != b.LeafID {
		t.Fatalf("consistent hashing must be sticky, got %d then %d", a.LeafID, b.LeafID)
	}
	if a.Reason != ReasonConsistentHashingHome {
		t.Fatalf("reason = %s", a.Reason)
	}
}

func TestConsistentHashingSkipsUnhealthyLeaves(t *testing.T) {
	ids := []uint64{1, 2, 3, 4}
	states := healthy(ids...)
	// Leaf 2 becomes unhealthy: it must never be selected.
	states[1].HealthyWorkers = 0
	picker := modelFor(t, consistentCfg(), ids, states).Picker()
	for i := uint64(0); i < 50; i++ {
		target, err := picker.Pick(RouteRequest{FunctionID: i})
		if err != nil {
			t.Fatal(err)
		}
		if target.LeafID == 2 {
			t.Fatalf("unhealthy leaf 2 was selected for function %d", i)
		}
	}
}

func TestLeastLoadedPicksLowestAggregateInFlight(t *testing.T) {
	ids := []uint64{1, 2, 3}
	states := []LeafState{
		{LeafID: 1, FullSnapshot: true, HealthyWorkers: 1, AggregateInFlight: 9},
		{LeafID: 2, FullSnapshot: true, HealthyWorkers: 1, AggregateInFlight: 2},
		{LeafID: 3, FullSnapshot: true, HealthyWorkers: 1, AggregateInFlight: 5},
	}
	picker := modelFor(t, leastLoadedCfg(), ids, states).Picker()
	target, err := picker.Pick(RouteRequest{FunctionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != 2 {
		t.Fatalf("least-loaded picked %d, want 2", target.LeafID)
	}
}

func TestBoundedLoadsForwardsFromHotHome(t *testing.T) {
	ids := []uint64{1, 2, 3, 4}
	const fn = uint64(10)
	home := chbl.NewRing(ids).Home(fn)

	states := healthy(ids...)
	for i := range states {
		if states[i].LeafID == home {
			states[i].LeafLoad = 5.0 // above the bound of 1.0
		}
	}
	picker := modelFor(t, boundedLoadsCfg(1.0, 3), ids, states).Picker()
	target, err := picker.Pick(RouteRequest{FunctionID: fn})
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID == home {
		t.Fatalf("expected forwarding off the hot home leaf %d, got %d (reason=%s)", home, target.LeafID, target.Reason)
	}
	if target.Reason != ReasonBoundedLoadsForwarded && target.Reason != ReasonBoundedLoadsLeastLoaded {
		t.Fatalf("unexpected reason %s", target.Reason)
	}
}

func TestAvailableCapacityPrefersWarmLeaf(t *testing.T) {
	ids := []uint64{1, 2}
	const fn = uint64(7)
	states := []LeafState{
		{LeafID: 1, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{
			{FunctionId: fn, AvailableConcurrency: 0},
		}},
		{LeafID: 2, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{
			{FunctionId: fn, AvailableConcurrency: 4},
		}},
	}
	picker := modelFor(t, availableCapacityCfg(), ids, states).Picker()
	target, err := picker.Pick(RouteRequest{FunctionID: fn})
	if err != nil {
		t.Fatal(err)
	}
	if target.LeafID != 2 {
		t.Fatalf("available-capacity picked %d, want the warm leaf 2", target.LeafID)
	}

	// A function no leaf reports falls back to a healthy leaf.
	cold, err := picker.Pick(RouteRequest{FunctionID: 999})
	if err != nil {
		t.Fatal(err)
	}
	if cold.LeafID != 1 && cold.LeafID != 2 {
		t.Fatalf("cold fallback picked unknown leaf %d", cold.LeafID)
	}
}

func TestAvailableCapacityUpdatesSparseLeaves(t *testing.T) {
	model := modelFor(t, availableCapacityCfg(), []uint64{7, 41}, []LeafState{
		{LeafID: 7, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: 2}}},
		{LeafID: 41, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: 1}}},
	})
	picker := model.Picker()
	if got := model.Apply(LeafState{LeafID: 41, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: 5}}}); got != ChangedInPlace {
		t.Fatalf("value update = %v, want ChangedInPlace", got)
	}
	target, err := picker.Pick(RouteRequest{FunctionID: 3})
	if err != nil || target.LeafID != 41 {
		t.Fatalf("updated picker returned %+v, %v", target, err)
	}
	if got := model.ReplaceLeaf(LeafState{LeafID: 41, FullSnapshot: true, HealthyWorkers: 1}); got != ChangedInPlace {
		t.Fatalf("replacement = %v, want ChangedInPlace", got)
	}
	target, err = picker.Pick(RouteRequest{FunctionID: 3})
	if err != nil || target.LeafID != 7 {
		t.Fatalf("removed route returned %+v, %v", target, err)
	}
}

func TestRoutingControllerRejectsOldFrames(t *testing.T) {
	top := testTopology(7)
	c := NewRoutingController(ControllerConfig{Topology: top})
	c.model = roundRobinPolicy{}.NewModel(top)
	c.version = 2
	c.serving = true
	c.lastRevision = make(map[uint64]uint64)
	c.engine.install(c.model.Picker(), 2)
	if c.onFrame(LeafState{ConfigVersion: 1, LeafID: 7, Revision: 9, FullSnapshot: true, HealthyWorkers: 1}) {
		t.Fatal("accepted an old configuration frame")
	}
	if !c.onFrame(LeafState{ConfigVersion: 2, LeafID: 7, Revision: 10, FullSnapshot: true, HealthyWorkers: 1}) {
		t.Fatal("rejected the active baseline")
	}
	if c.onFrame(LeafState{ConfigVersion: 2, LeafID: 7, Revision: 8, FullSnapshot: true}) {
		t.Fatal("accepted an older full snapshot")
	}
	c.markUnhealthy(7, 1)
	if target, err := c.Engine().Pick(RouteRequest{}); err != nil || target.LeafID != 7 {
		t.Fatalf("stale update changed routing: %+v, %v", target, err)
	}
}

func TestAvailableCapacityConcurrentPickAndUpdate(t *testing.T) {
	model := modelFor(t, availableCapacityCfg(), []uint64{7, 41}, []LeafState{
		{LeafID: 7, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: 1}}},
		{LeafID: 41, FullSnapshot: true, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: 2}}},
	})
	picker := model.Picker()
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 1000 {
				target, err := picker.Pick(RouteRequest{FunctionID: 3})
				if err != nil || (target.LeafID != 7 && target.LeafID != 41) {
					t.Errorf("concurrent pick returned %+v, %v", target, err)
					return
				}
			}
		}()
	}
	for i := uint64(0); i < 1000; i++ {
		model.Apply(LeafState{LeafID: 7, HealthyWorkers: 1, Capacities: []*core.FunctionCapacity{{FunctionId: 3, AvailableConcurrency: i}}})
	}
	readers.Wait()
}
