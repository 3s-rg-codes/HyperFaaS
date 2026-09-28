package routing

// Policy scaling harness.
//
// It measures how the cost of each routing policy grows along the dimensions
// of a HyperFaaS deployment:
//
//	L   leaves known to one ingress replica
//	F   functions advertised with capacity (available capacity only)
//	Lf  leaves that advertise capacity for one function, i.e. how far the
//	    leaves have spread that function (available capacity only)
//
// policy_cost_test.go advertises every function on every leaf, which is the
// worst case Lf = L. Here every policy is swept over L, and available capacity,
// the only policy that depends on F and Lf, is swept over the full grid
// L x F x Lf with Lf <= L and F*Lf <= scalingMaxEntries. Run it with:
//
//	go test -run '^$' -bench 'BenchmarkScaling' -benchmem -count=10 ./pkg/ingress/routing/
//	HYPERFAAS_POLICY_COST=1 go test -run 'TestPolicyScaling' -v ./pkg/ingress/routing/

import (
	"fmt"
	"runtime"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

const (
	// scalingFunction is the function picked and updated by the benchmarks.
	scalingFunction = 7
	// scalingMaxEntries caps F*Lf. Larger grids need several GB to build and
	// exceed any deployment we model.
	scalingMaxEntries = 1 << 20
)

var (
	scalingLeaves    = []int{16, 64, 256, 1024, 4096}
	scalingFunctionN = []int{32, 1000, 10000}
	scalingSpreads   = []int{1, 4, 16, 64, 256, 1024, 4096}
)

// scalingDim is one grid cell. F and Lf are zero for policies that ignore them.
type scalingDim struct{ L, F, Lf int }

func (d scalingDim) String() string {
	if d.F == 0 {
		return fmt.Sprintf("L=%d", d.L)
	}
	return fmt.Sprintf("L=%d/F=%d/Lf=%d", d.L, d.F, d.Lf)
}

// scalingGrid returns the cells for one policy.
func scalingGrid(needs RoutingNeeds) []scalingDim {
	var dims []scalingDim
	for _, L := range scalingLeaves {
		if !needs.Has(NeedFunctionCapacity) {
			dims = append(dims, scalingDim{L: L})
			continue
		}
		for _, F := range scalingFunctionN {
			for _, Lf := range scalingSpreads {
				if Lf <= L && F*Lf <= scalingMaxEntries {
					dims = append(dims, scalingDim{L, F, Lf})
				}
			}
		}
	}
	return dims
}

func scalingCases() []routingCostCase {
	return []routingCostCase{
		{"random", randomCfg(), 0.5},
		{"round-robin", roundRobinCfg(), 0.5},
		{"consistent-hashing", consistentCfg(), 0.5},
		{"least-loaded", leastLoadedCfg(), 0.5},
		{"bounded-loads", boundedLoadsCfg(1.0, 3), 0.5},
		{"bounded-loads-all-over-bound", boundedLoadsCfg(1.0, 3), 5.0},
		{"rj-ch", randomJumpCfg(1.0, 3), 0.5},
		{"ch-rlu", chrluCfg(1.0, 3), 0.5},
		{"available-capacity", availableCapacityCfg(), 0.5},
	}
}

// spreadStart places function fn on Lf consecutive leaf indices starting here.
func spreadStart(fn uint64, L int) int {
	return int((fn * 2654435761) % uint64(L))
}

// spreadLeafStates builds L leaves where each of F functions is advertised by
// Lf leaves. Scalar signals are set as in costLeafStates.
func spreadLeafStates(L int, needs RoutingNeeds, F, Lf int, leafLoad float64) []LeafState {
	states := costLeafStates(L, needs&^NeedFunctionCapacity, 0, leafLoad)
	if !needs.Has(NeedFunctionCapacity) {
		return states
	}
	if Lf > L {
		Lf = L
	}
	for j := 1; j <= F; j++ {
		fn := uint64(j)
		start := spreadStart(fn, L)
		for k := 0; k < Lf; k++ {
			i := (start + k) % L
			states[i].Capacities = append(states[i].Capacities, costCapacity(fn, costTimestamp(j)))
		}
	}
	return states
}

// scalingModel builds a policy model at (L, F, Lf) and returns it with its picker.
func scalingModel(c routingCostCase, L, F, Lf int) (routingModel, Picker, RoutingNeeds) {
	needs, err := NeedsFor(c.cfg)
	if err != nil {
		panic(err)
	}
	m, p := costBuild(c.cfg, costTopology(L), spreadLeafStates(L, needs, F, Lf, c.load))
	return m, p, needs
}

// scalingDelta returns a one-leaf frame that changes every signal the policy
// reads. For available capacity it changes scalingFunction on a leaf that
// advertises it.
func scalingDelta(needs RoutingNeeds, L int) LeafState {
	leaf := uint64(1)
	if needs.Has(NeedFunctionCapacity) {
		leaf = uint64(spreadStart(scalingFunction, L) + 1)
	}
	delta := LeafState{LeafID: leaf, HealthyWorkers: 1}
	if needs.Has(NeedLeafLoad) {
		delta.HasLeafLoad = true
	}
	if needs.Has(NeedAggregateInFlight) {
		delta.HasAggregateInFlight = true
	}
	if needs.Has(NeedFunctionCapacity) {
		delta.Capacities = []*core.FunctionCapacity{{FunctionId: scalingFunction, ReadyInstances: 1}}
	}
	return delta
}

func benchPick(b *testing.B, picker Picker) {
	req := RouteRequest{UserID: 1, FunctionID: scalingFunction}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := picker.Pick(req); err != nil {
			b.Fatal(err)
		}
	}
}

func benchUpdate(b *testing.B, model routingModel, picker Picker, needs RoutingNeeds, L int) {
	delta := scalingDelta(needs, L)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
}

// scalingRun runs fn for every policy and grid cell.
func scalingRun(b *testing.B, fn func(b *testing.B, c routingCostCase, d scalingDim)) {
	for _, c := range scalingCases() {
		needs, err := NeedsFor(c.cfg)
		if err != nil {
			b.Fatal(err)
		}
		for _, d := range scalingGrid(needs) {
			b.Run(fmt.Sprintf("policy=%s/%s", c.name, d), func(b *testing.B) { fn(b, c, d) })
		}
	}
}

// BenchmarkScalingPick: cost of one routing decision.
func BenchmarkScalingPick(b *testing.B) {
	scalingRun(b, func(b *testing.B, c routingCostCase, d scalingDim) {
		_, picker, _ := scalingModel(c, d.L, d.F, d.Lf)
		benchPick(b, picker)
	})
}

// BenchmarkScalingUpdate: cost of applying one state frame.
func BenchmarkScalingUpdate(b *testing.B) {
	scalingRun(b, func(b *testing.B, c routingCostCase, d scalingDim) {
		model, picker, needs := scalingModel(c, d.L, d.F, d.Lf)
		benchUpdate(b, model, picker, needs, d.L)
	})
}

// BenchmarkScalingHealth: cost of a leaf health change.
func BenchmarkScalingHealth(b *testing.B) {
	scalingRun(b, func(b *testing.B, c routingCostCase, d scalingDim) {
		model, picker, _ := scalingModel(c, d.L, d.F, d.Lf)
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

// TestPolicyScalingRetained reports retained policy state for every grid cell.
func TestPolicyScalingRetained(t *testing.T) {
	requireCostHarness(t)
	t.Logf("policy\tL\tF\tLf\tretained_bytes")
	for _, c := range scalingCases() {
		needs, err := NeedsFor(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range scalingGrid(needs) {
			top := costTopology(d.L)
			ret := measureRetained(func() any {
				m, p := costBuild(c.cfg, top, spreadLeafStates(d.L, needs, d.F, d.Lf, c.load))
				return retainedState{model: m, picker: p}
			})
			t.Logf("retained\t%s\t%d\t%d\t%d\t%d", c.name, d.L, d.F, d.Lf, ret)
		}
	}
}
