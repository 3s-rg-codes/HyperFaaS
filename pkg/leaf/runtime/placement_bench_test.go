package runtime

import (
	"context"
	"fmt"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf/scheduler"
)

func benchWorkers(n int) []*core.WorkerState {
	workers := make([]*core.WorkerState, n)
	for i := range workers {
		workers[i] = &core.WorkerState{
			WorkerId:    uint64(i + 1),
			Healthy:     true,
			Schedulable: true,
			Capacity:    &core.ResourceSpec{CpuUnits: 2000, MemoryBytes: 8 << 30, DiskBytes: 50 << 30},
			Allocated:   &core.ResourceUsage{CpuUnits: uint64(i%20) * 100},
			Instances:   uint64(i % 20),
		}
	}
	return workers
}

// BenchmarkPlacementSchedule measures PlacementState.Schedule end-to-end: the
// placement snapshot overlay (which today clones every worker protobuf), the
// scheduler pick, and reservation bookkeeping. It is the cost paid once per
// sandbox start. Cancel keeps the state at steady scale so the benchmark does
// not trend.
func BenchmarkPlacementSchedule(b *testing.B) {
	function := &core.FunctionSpec{
		FunctionId: 1,
		Runtime:    &core.RuntimeSpec{Image: "fake://echo"},
	}
	for _, workers := range []int{2, 64, 1024, 16384} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			states := benchWorkers(workers)
			state := NewPlacementState(workers, 0, 0, 0)
			for i, worker := range states {
				state.UpdateWorker(i, worker)
			}
			sched := scheduler.NewBalancedRoundRobin(workers, 0)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, reservation, _, _, err := state.Schedule(ctx, sched, function, states, nil)
				if err != nil {
					b.Fatal(err)
				}
				if !reservation.Valid {
					b.Fatal("no reservation")
				}
				state.CancelReservation(reservation)
			}
		})
	}
}

// BenchmarkPlacementScheduleParallel measures concurrent placement from many
// functions. Today every call serializes on PlacementState.mu, so this exposes
// the global placement critical section under the race-free benchmark.
func BenchmarkPlacementScheduleParallel(b *testing.B) {
	const workers = 64
	function := &core.FunctionSpec{
		FunctionId: 1,
		Runtime:    &core.RuntimeSpec{Image: "fake://echo"},
	}
	states := benchWorkers(workers)
	state := NewPlacementState(workers, 0, 0, 0)
	for i, worker := range states {
		state.UpdateWorker(i, worker)
	}
	sched := scheduler.NewBalancedRoundRobin(workers, 0)
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, reservation, _, _, err := state.Schedule(ctx, sched, function, states, nil)
			if err != nil {
				b.Error(err)
				return
			}
			if !reservation.Valid {
				b.Error("no reservation")
				return
			}
			state.CancelReservation(reservation)
		}
	})
}
