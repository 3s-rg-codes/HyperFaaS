package routing

// Policy cost measurement harness.
//
// It measures the three quantities of the cost model in
// docs/ROUTING_POLICY_COST_RESULTS.md for the state-gated routing policies:
//
//	retained  bytes that live in the process while a policy is active
//	          (policy model + immutable picker);
//	alloc     bytes allocated to build that state once (allocation volume);
//	churn     bytes allocated per state update, including any publication;
//	decision  work per Pick;
//	wire      proto bytes a subscriber receives per frame.
//
// It is deliberately in-package so it can build the unexported policy models
// and pickers directly. Run it with:
//
//	just policy-cost
//	HYPERFAAS_POLICY_COST=1 go test -run 'TestPolicyCost' -v ./pkg/ingress/routing/
//	go test -run '^$' -bench 'BenchmarkRouting' -benchmem ./pkg/ingress/routing/
//
// The retained tests are gated by HYPERFAAS_POLICY_COST so `go test ./...` stays
// fast. The numbers are host- and topology-specific. Compare policies at the
// same dimensions, not absolute values.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
)

// requireCostHarness skips the allocation-heavy measurements unless explicitly
// requested, so `go test ./...` stays fast. `just policy-cost` sets the env.
func requireCostHarness(t *testing.T) {
	t.Helper()
	if os.Getenv("HYPERFAAS_POLICY_COST") == "" {
		t.Skip("set HYPERFAAS_POLICY_COST=1 or run `just policy-cost`")
	}
}

// routingCostCase is one policy plus the leaf-load level used to build it. A
// high load forces the bounded-loads overflow path, which is otherwise
// invisible when every leaf is under the bound.
type routingCostCase struct {
	name string
	cfg  *core.RoutingPolicyConfig
	load float64
}

func routingCostCases() []routingCostCase {
	return []routingCostCase{
		{"random", randomCfg(), 0.5},
		{"round-robin", roundRobinCfg(), 0.5},
		{"consistent-hashing", consistentCfg(), 0.5},
		{"least-loaded", leastLoadedCfg(), 0.5},
		{"bounded-loads", boundedLoadsCfg(1.0, 3), 0.5},
		{"bounded-loads-hot", boundedLoadsCfg(1.0, 3), 5.0},
		{"available-capacity", availableCapacityCfg(), 0.5},
	}
}

// costCapacity mirrors pkg/leaf/runtime.Runtime.CapacitySnapshot so wire and
// retention sizes reflect what a leaf actually builds.
func costCapacity(fnID uint64, observed *timestamppb.Timestamp) *core.FunctionCapacity {
	return &core.FunctionCapacity{
		FunctionId:           fnID,
		ReadyInstances:       uint32(1 + fnID%4),
		AvailableConcurrency: fnID % 8,
		InFlight:             fnID % 3,
		Status:               core.CapacityStatus_CAPACITY_STATUS_AVAILABLE,
		ObservedAt:           observed,
	}
}

// costTopology builds a static leaf topology. It is configured state that
// exists regardless of policy, so it is built outside the measured closures.
func costTopology(L int) Topology {
	addrs := make(map[uint64]LeafAddress, L)
	for i := 0; i < L; i++ {
		id := uint64(i + 1)
		addrs[id] = LeafAddress{
			HTTPAddress:    fmt.Sprintf("http://leaf-%d:8080", id),
			ControlAddress: fmt.Sprintf("leaf-%d:9010", id),
		}
	}
	return NewTopology(addrs)
}

// costLeafStates builds L leaves. Each leaf advertises `functions` function
// capacities when the policy requests that projection. It is called inside the
// measured closures so capacity retention is attributed to the policy.
func costLeafStates(L int, needs RoutingNeeds, functions int, leafLoad float64) []LeafState {
	states := make([]LeafState, L)
	for i := 0; i < L; i++ {
		id := uint64(i + 1)
		s := LeafState{LeafID: id, FullSnapshot: true, HealthyWorkers: 1}
		if needs.Has(NeedLeafLoad) {
			s.LeafLoad = leafLoad
			s.HasLeafLoad = true
		}
		if needs.Has(NeedAggregateInFlight) {
			s.AggregateInFlight = uint64((i % 7) + 1)
			s.HasAggregateInFlight = true
		}
		if needs.Has(NeedFunctionCapacity) {
			caps := make([]*core.FunctionCapacity, functions)
			for j := 0; j < functions; j++ {
				// pkg/leaf/runtime.CapacitySnapshot calls timestamppb.Now() once
				// per function, so every entry carries its own timestamp.
				caps[j] = costCapacity(uint64(j+1), costTimestamp(j))
			}
			s.Capacities = caps
		}
		states[i] = s
	}
	return states
}

var costObservedBase = time.Unix(1_700_000_000, 0)

// costTimestamp returns a fresh timestamp so per-entry retention reflects the
// leaf's per-function observed_at allocation.
func costTimestamp(i int) *timestamppb.Timestamp {
	return timestamppb.New(costObservedBase.Add(time.Duration(i) * time.Millisecond))
}

func costBuild(cfg *core.RoutingPolicyConfig, top Topology, states []LeafState) (routingModel, Picker) {
	policy, err := newRoutingPolicy(cfg)
	if err != nil {
		panic(err)
	}
	m := policy.NewModel(top)
	for _, s := range states {
		m.ReplaceLeaf(s)
	}
	return m, m.Picker()
}

// retainedState keeps both the mutable model and the immutable picker alive so
// the heap measurement covers the whole active-policy footprint.
type retainedState struct {
	model  routingModel
	picker Picker
}

func measureRetained(build func() any) int64 {
	deltas := make([]int64, 0, 9)
	for r := 0; r < 9; r++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		held := build()
		runtime.GC()
		runtime.ReadMemStats(&m1)
		deltas = append(deltas, int64(m1.HeapAlloc)-int64(m0.HeapAlloc))
		runtime.KeepAlive(held)
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	d := deltas[len(deltas)/2]
	if d < 0 {
		return 0
	}
	return d
}

func measureAlloc(build func() any) int64 {
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	held := build()
	runtime.ReadMemStats(&m1)
	runtime.KeepAlive(held)
	return int64(m1.TotalAlloc - m0.TotalAlloc)
}

// ungatedModel is the union of every policy's state, i.e. what an ingress
// replica had to retain before gating: membership + ring + loads + aggregate
// in-flight + per-function capacity. It is a modeled baseline, not a code path.
type ungatedModel struct {
	membership membership
	ring       *chbl.Ring
	loads      map[uint64]float64
	inFlight   map[uint64]uint64
	caps       map[uint64]map[uint64]*core.FunctionCapacity
}

func buildUngated(top Topology, states []LeafState) *ungatedModel {
	g := &ungatedModel{
		membership: newMembership(top),
		loads:      make(map[uint64]float64, len(states)),
		inFlight:   make(map[uint64]uint64, len(states)),
		caps:       make(map[uint64]map[uint64]*core.FunctionCapacity, len(states)),
	}
	ids := make([]uint64, 0, len(states))
	for _, s := range states {
		g.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
		g.loads[s.LeafID] = s.LeafLoad
		g.inFlight[s.LeafID] = s.AggregateInFlight
		byFn := make(map[uint64]*core.FunctionCapacity, len(s.Capacities))
		for _, c := range s.Capacities {
			byFn[c.GetFunctionId()] = c
		}
		g.caps[s.LeafID] = byFn
		ids = append(ids, s.LeafID)
	}
	g.ring = chbl.NewRing(ids)
	return g
}

func TestPolicyCostSizing(t *testing.T) {
	t.Logf("record\tbytes")
	t.Logf("LeafTarget\t%d", unsafe.Sizeof(LeafTarget{}))
	t.Logf("LeafAddress\t%d", unsafe.Sizeof(LeafAddress{}))
	t.Logf("FunctionCapacity\t%d", unsafe.Sizeof(core.FunctionCapacity{}))
	t.Logf("Timestamp\t%d", unsafe.Sizeof(timestamppb.Timestamp{}))
	t.Logf("chbl.Ring\t%d", unsafe.Sizeof(chbl.Ring{}))
	t.Logf("ringNode(inferred)\t%d", 16)
	t.Logf("retainedState\t%d", unsafe.Sizeof(retainedState{}))
}

func TestPolicyCostCHBLRing(t *testing.T) {
	requireCostHarness(t)
	for _, L := range []int{1, 16, 256, 4096} {
		ids := make([]uint64, L)
		for i := range ids {
			ids[i] = uint64(i + 1)
		}
		alloc := measureAlloc(func() any { return chbl.NewRing(ids) })
		ret := measureRetained(func() any { return chbl.NewRing(ids) })
		t.Logf("ring\tL=%d\talloc_bytes=%d\tretained_bytes=%d\tbytesPerLeaf=%d", L, alloc, ret, ret/int64(L))
	}
}

// TestPolicyCostRetained reports the active-policy footprint for every policy.
// A=0 means the policy retains no per-function capacity; AC is measured at a
// few function counts because its footprint is O(L*A).
func TestPolicyCostRetained(t *testing.T) {
	requireCostHarness(t)
	type dim struct{ L, A int }
	base := []dim{{1, 0}, {16, 0}, {256, 0}, {1024, 0}, {4096, 0}}
	acDims := []dim{{1, 1}, {16, 1}, {16, 32}, {256, 32}, {256, 128}, {1024, 32}, {4096, 1}, {4096, 32}}

	t.Logf("policy\tL\tA\tretained_bytes\talloc_bytes")
	for _, c := range routingCostCases() {
		needs, err := NeedsFor(c.cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		dims := base
		if needs.Has(NeedFunctionCapacity) {
			dims = acDims
		}
		for _, d := range dims {
			top := costTopology(d.L)
			ret := measureRetained(func() any {
				states := costLeafStates(d.L, needs, d.A, c.load)
				m, p := costBuild(c.cfg, top, states)
				return retainedState{model: m, picker: p}
			})
			alloc := measureAlloc(func() any {
				states := costLeafStates(d.L, needs, d.A, c.load)
				m, p := costBuild(c.cfg, top, states)
				return retainedState{model: m, picker: p}
			})
			t.Logf("%s\t%d\t%d\t%d\t%d", c.name, d.L, d.A, ret, alloc)
		}
	}

	// Modeled pre-gating baseline: one replica retains every signal.
	t.Logf("ungated (model)\tL\tA\tretained_bytes\talloc_bytes")
	for _, d := range []dim{{16, 32}, {256, 32}, {256, 128}, {4096, 32}} {
		top := costTopology(d.L)
		ret := measureRetained(func() any {
			states := costLeafStates(d.L, NeedFunctionCapacity|NeedLeafLoad|NeedAggregateInFlight, d.A, 0.5)
			return buildUngated(top, states)
		})
		alloc := measureAlloc(func() any {
			states := costLeafStates(d.L, NeedFunctionCapacity|NeedLeafLoad|NeedAggregateInFlight, d.A, 0.5)
			return buildUngated(top, states)
		})
		t.Logf("ungated\t%d\t%d\t%d\t%d", d.L, d.A, ret, alloc)
	}
}

// TestPolicyCostWire reports proto bytes per frame for the projection each
// policy actually requests. A full frame is the stream baseline; a delta frame
// carries only changed capacities plus any requested scalars.
func TestPolicyCostWire(t *testing.T) {
	t.Logf("policy\tneeds\tfull_A\tdelta_A\tfull_bytes\tdelta_bytes")
	for _, c := range routingCostCases() {
		needs, _ := NeedsFor(c.cfg)
		for _, functions := range []int{1, 32, 128, 1000} {
			full := wireFrameBytes(needs, functions, true)
			delta := wireFrameBytes(needs, functions, false)
			t.Logf("%s\t%b\t%d\t%d\t%d\t%d", c.name, needs, functions, 0, full, delta)
		}
	}

	// Ungated: every frame carries every signal regardless of policy.
	for _, functions := range []int{1, 32, 128, 1000} {
		full := wireFrameBytes(NeedFunctionCapacity|NeedLeafLoad|NeedAggregateInFlight, functions, true)
		delta := wireFrameBytes(NeedFunctionCapacity|NeedLeafLoad|NeedAggregateInFlight, functions, false)
		t.Logf("ungated-full\t-\t%d\t%d\t%d\t%d", functions, 0, full, delta)
	}
}

func wireFrameBytes(needs RoutingNeeds, capacities int, full bool) int {
	f := &leafpkg.RoutingStateFrame{
		ConfigVersion: 42, LeafId: 1, Revision: 7,
		FullSnapshot: full, HealthyWorkers: 3,
	}
	if needs.Has(NeedLeafLoad) {
		v := 0.75
		f.LeafLoad = &v
	}
	if needs.Has(NeedAggregateInFlight) {
		v := uint64(123)
		f.AggregateInFlight = &v
	}
	if needs.Has(NeedFunctionCapacity) {
		n := capacities
		if !full {
			n = 0
		}
		observed := timestamppb.New(time.Unix(1_700_000_000, 0))
		for j := 0; j < n; j++ {
			f.Capacities = append(f.Capacities, costCapacity(uint64(j+1), observed))
		}
	}
	return proto.Size(f)
}

// BenchmarkRoutingPick measures per-decision work. The bounded-loads-hot case
// forces Forward past the home leaf, which exercises firstPosition/Next.
func BenchmarkRoutingPick(b *testing.B) {
	for _, L := range []int{1, 4, 16, 64, 256, 1024, 4096} {
		for _, c := range routingCostCases() {
			b.Run(fmt.Sprintf("L=%d/%s", L, c.name), func(b *testing.B) {
				needs, _ := NeedsFor(c.cfg)
				A := 0
				if needs.Has(NeedFunctionCapacity) {
					A = 32
				}
				top := costTopology(L)
				states := costLeafStates(L, needs, A, c.load)
				_, picker := costBuild(c.cfg, top, states)
				req := RouteRequest{UserID: 1, FunctionID: 7}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := picker.Pick(req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkRoutingChurn measures one accepted frame, including a new
// picker only when the policy requests one. The incoming frame is prepared
// outside the timed loop; this measures policy maintenance, not proto creation.
func BenchmarkRoutingChurn(b *testing.B) {
	for _, L := range []int{1, 16, 256, 1024} {
		for _, c := range routingCostCases() {
			b.Run(fmt.Sprintf("L=%d/%s", L, c.name), func(b *testing.B) {
				needs, _ := NeedsFor(c.cfg)
				A := 0
				if needs.Has(NeedFunctionCapacity) {
					A = 32
				}
				top := costTopology(L)
				states := costLeafStates(L, needs, A, c.load)
				model, picker := costBuild(c.cfg, top, states)

				delta := LeafState{LeafID: 1, FullSnapshot: false, HealthyWorkers: 1}
				if needs.Has(NeedLeafLoad) {
					delta.LeafLoad = 0.9
					delta.HasLeafLoad = true
				}
				if needs.Has(NeedAggregateInFlight) {
					delta.AggregateInFlight = 99
					delta.HasAggregateInFlight = true
				}
				if needs.Has(NeedFunctionCapacity) {
					delta.Capacities = []*core.FunctionCapacity{{FunctionId: 7, ReadyInstances: 1}}
				}

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// A membership-only frame repeats unchanged state, so the
					// policy reports no change and the picker is not rebuilt. A
					// load/capacity frame carries a new value every time.
					tick := uint64(i) + 1
					if needs.Has(NeedLeafLoad) {
						delta.LeafLoad = 0.5 + float64(tick&1)
					}
					if needs.Has(NeedAggregateInFlight) {
						delta.AggregateInFlight = tick
					}
					if needs.Has(NeedFunctionCapacity) {
						delta.Capacities[0].AvailableConcurrency = tick
					}
					// Mirror RoutingController.onFrame's publication decision.
					if model.Apply(delta) == PublishPicker {
						picker = model.Picker()
					}
				}
				runtime.KeepAlive(picker)
			})
		}
	}
}

// BenchmarkRoutingHealthChange measures the less frequent membership path.
// It includes ring rebuilds for the policies that use a healthy-leaf ring.
func BenchmarkRoutingHealthChange(b *testing.B) {
	for _, L := range []int{16, 256, 1024} {
		for _, c := range routingCostCases() {
			b.Run(fmt.Sprintf("L=%d/%s", L, c.name), func(b *testing.B) {
				needs, _ := NeedsFor(c.cfg)
				A := 0
				if needs.Has(NeedFunctionCapacity) {
					A = 32
				}
				model, picker := costBuild(c.cfg, costTopology(L), costLeafStates(L, needs, A, c.load))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var result UpdateResult
					if i&1 == 0 {
						result = model.LeafDisconnected(1)
					} else {
						result = model.Apply(LeafState{LeafID: 1, HealthyWorkers: 1})
					}
					if result == PublishPicker {
						picker = model.Picker()
					}
				}
				runtime.KeepAlive(picker)
			})
		}
	}
}
