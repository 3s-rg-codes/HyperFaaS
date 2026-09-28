package scheduler

// Placement (leaf-tier) cost measurement harness.
//
// It mirrors pkg/ingress/routing/policy_cost_test.go one tier down: for every
// placement policy it reports the gated worker signals it reads
// (PlacementNeeds), the worker-state wire footprint with and without those
// signals, and per-decision work. See docs/POLICY_COST_ANALYSIS.md.
//
//	go test -run 'TestPlacementCost' -v ./pkg/leaf/scheduler/
//	go test -run '^$' -bench 'BenchmarkPlacementPick' -benchmem ./pkg/leaf/scheduler/
//
// Focused on worker BoundedLoads and ImageAware because the existing
// BenchmarkPickWorker does not exercise them.

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"unsafe"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
)

func placementCostCases(workerCount int) []struct {
	name string
	s    PlacementScheduler
} {
	return []struct {
		name string
		s    PlacementScheduler
	}{
		{"balanced-round-robin", NewBalancedRoundRobin(workerCount, 0)},
		{"resource-aware", NewResourceAware(0)},
		{"cold-start-aware", NewColdStartAware(0)},
		{"reservation-aware", newCostReservationAware(0)},
		{"image-aware", NewImageAware(0)},
		{"bounded-loads", NewBoundedLoads(workerCount, 0, 1.0, 3)},
	}
}

// newCostReservationAware uses a cycling index instead of a constant one.
// ReservationAware requires two distinct random eligible indexes, so a constant
// index makes its `for second == first` loop spin forever.
func newCostReservationAware(maxInstances int) PlacementScheduler {
	next := 0
	return NewReservationAwareWithRand(maxInstances, func(n int) int {
		v := next % n
		next++
		return v
	})
}

func placementCfgCases() []struct {
	name string
	cfg  *core.PlacementPolicyConfig
} {
	return []struct {
		name string
		cfg  *core.PlacementPolicyConfig
	}{
		{"balanced-round-robin", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BalancedRoundRobin{BalancedRoundRobin: &core.BalancedRoundRobinPlacement{}}}},
		{"resource-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ResourceAware{ResourceAware: &core.ResourceAwarePlacement{}}}},
		{"cold-start-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ColdStartAware{ColdStartAware: &core.ColdStartAwarePlacement{}}}},
		{"reservation-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ReservationAware{ReservationAware: &core.ReservationAwarePlacement{}}}},
		{"image-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ImageAware{ImageAware: &core.ImageAwarePlacement{}}}},
		{"bounded-loads", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BoundedLoads{BoundedLoads: &core.BoundedLoadsPlacement{Bound: 1.0, MaxChainLen: 3}}}},
	}
}

// costWorkers builds W worker states with optional cached images and a load
// signal. All fields are populated; the projection gating is modeled separately
// by the wire and retention helpers.
func costWorkers(n, imagesPerWorker int) []*core.WorkerState {
	workers := make([]*core.WorkerState, n)
	for i := range workers {
		w := &core.WorkerState{
			WorkerId:           uint64(i + 1),
			Healthy:            true,
			Schedulable:        true,
			Capacity:           &core.ResourceSpec{CpuUnits: 2000, MemoryBytes: 8 << 30, DiskBytes: 50 << 30},
			Allocated:          &core.ResourceUsage{CpuUnits: uint64(i%20) * 100, MemoryBytes: uint64(i%20) * (128 << 20), DiskBytes: uint64(i%20) * (64 << 20)},
			Instances:          uint64(i % 20),
			ColdStartsInFlight: uint32(i % 3),
			LoadAverageNorm:    float64(i%10) / 10,
		}
		for j := 0; j < imagesPerWorker; j++ {
			w.CachedImages = append(w.CachedImages, &core.CachedImage{
				Image:    fmt.Sprintf("img-%d:latest", j),
				Digest:   fmt.Sprintf("sha256:%064d", j),
				CachedAt: timestamppb.Now(),
			})
		}
		workers[i] = w
	}
	return workers
}

func costFunction() *core.FunctionSpec {
	return &core.FunctionSpec{
		FunctionId: 1,
		Runtime: &core.RuntimeSpec{
			Image:     "img-0:latest",
			Resources: &core.ResourceSpec{CpuUnits: 100, MemoryBytes: 128 << 20, DiskBytes: 64 << 20},
		},
	}
}

func TestPlacementCostNeeds(t *testing.T) {
	t.Logf("placement_policy\tneeds\tgated_signals")
	for _, c := range placementCfgCases() {
		n, err := NeedsFor(c.cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		signals := "none (lifecycle only)"
		switch {
		case n.Has(NeedWorkerLoad) && n.Has(NeedWorkerImages):
			signals = "load_average_norm + cached_images"
		case n.Has(NeedWorkerLoad):
			signals = "load_average_norm"
		case n.Has(NeedWorkerImages):
			signals = "cached_images"
		}
		t.Logf("%s\t%b\t%s", c.name, n, signals)
	}
}

func TestPlacementCostSizing(t *testing.T) {
	t.Logf("record\tbytes")
	t.Logf("WorkerState\t%d", unsafe.Sizeof(core.WorkerState{}))
	t.Logf("InstanceState\t%d", unsafe.Sizeof(core.InstanceState{}))
	t.Logf("CachedImage\t%d", unsafe.Sizeof(core.CachedImage{}))
	t.Logf("ResourceSpec\t%d", unsafe.Sizeof(core.ResourceSpec{}))
	t.Logf("ResourceUsage\t%d", unsafe.Sizeof(core.ResourceUsage{}))
}

// TestPlacementCostWire reports the WorkerState proto size with and without the
// gated signals. Sandboxes are always sent; load and images are gated.
func TestPlacementCostWire(t *testing.T) {
	const images = 8
	t.Logf("projection\tsandboxes\timages\tbytes")
	for _, sandboxes := range []int{1, 32, 256} {
		base := workerStateWire(sandboxes, false, images, false)
		load := workerStateWire(sandboxes, true, images, false)
		imgs := workerStateWire(sandboxes, false, images, true)
		both := workerStateWire(sandboxes, true, images, true)
		t.Logf("lifecycle-only\t%d\t%d\t%d", sandboxes, images, base)
		t.Logf("+load\t%d\t%d\t%d", sandboxes, images, load)
		t.Logf("+images\t%d\t%d\t%d", sandboxes, images, imgs)
		t.Logf("+load+images\t%d\t%d\t%d", sandboxes, images, both)
	}
}

func workerStateWire(sandboxes int, load bool, imageCount int, images bool) int {
	w := &core.WorkerState{
		WorkerId:    1,
		Healthy:     true,
		Schedulable: true,
		Capacity:    &core.ResourceSpec{CpuUnits: 2000, MemoryBytes: 8 << 30, DiskBytes: 50 << 30},
		Allocated:   &core.ResourceUsage{CpuUnits: 500, MemoryBytes: 1 << 30, DiskBytes: 2 << 30},
		Instances:   uint64(sandboxes),
	}
	w.SandboxStates = make([]*core.InstanceState, sandboxes)
	for i := range w.SandboxStates {
		w.SandboxStates[i] = &core.InstanceState{
			InstanceId: uint64(i + 1), FunctionId: 1, WorkerId: 1,
			Address: "10.0.0.2:50052", Protocol: "http", Ready: true,
			InFlight: uint64(i % 4), AvailableConcurrency: uint64(i % 8),
			StartedAt: timestamppb.Now(),
		}
	}
	if load {
		w.LoadAverageNorm = 0.5
	}
	if images {
		for j := 0; j < imageCount; j++ {
			w.CachedImages = append(w.CachedImages, &core.CachedImage{
				Image:  fmt.Sprintf("img-%d:latest", j),
				Digest: fmt.Sprintf("sha256:%064d", j),
			})
		}
	}
	return proto.Size(w)
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

// TestPlacementCostRetained reports the leaf-retained worker telemetry for a
// non-image-aware policy (lifecycle only) versus image-aware (with the cached
// image lists). The image lists are the gated part.
func TestPlacementCostRetained(t *testing.T) {
	t.Logf("policy\tworkers\timages\tretained_bytes")
	for _, W := range []int{16, 256, 1024} {
		lifecycle := measureRetained(func() any { return costWorkers(W, 0) })
		withImages := measureRetained(func() any { return costWorkers(W, 8) })
		t.Logf("non-image-aware\t%d\t0\t%d", W, lifecycle)
		t.Logf("image-aware\t%d\t8\t%d", W, withImages)
	}
}

// BenchmarkPlacementPick measures per-decision work for every placement policy,
// including the two the existing BenchmarkPickWorker omits.
func BenchmarkPlacementPick(b *testing.B) {
	for _, W := range []int{2, 64, 1024} {
		for _, c := range placementCostCases(W) {
			b.Run(fmt.Sprintf("W=%d/%s", W, c.name), func(b *testing.B) {
				workers := costWorkers(W, 8)
				function := costFunction()
				demand := &core.ScaleDemand{FunctionId: 1, DesiredInstances: 1}
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := c.s.PickWorker(ctx, function, workers, demand); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
