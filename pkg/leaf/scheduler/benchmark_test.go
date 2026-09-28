package scheduler

import (
	"context"
	"fmt"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

func BenchmarkPickWorker(b *testing.B) {
	for _, workers := range []int{2, 64, 1024, 16384} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			states := benchmarkWorkers(workers)
			function := &core.FunctionSpec{Runtime: &core.RuntimeSpec{Resources: &core.ResourceSpec{CpuUnits: 100, MemoryBytes: 128 << 20, DiskBytes: 64 << 20}}}
			demand := &core.ScaleDemand{FunctionId: 1, DesiredInstances: 1}
			nextReservationIndex := 0
			for _, tc := range []struct {
				name string
				s    PlacementScheduler
			}{
				{name: "balanced-round-robin", s: NewBalancedRoundRobin(workers, 0)},
				{name: "resource-aware", s: NewResourceAware(0)},
				{name: "cold-start-aware", s: NewColdStartAware(0)},
				{name: "reservation-aware", s: NewReservationAwareWithRand(0, func(n int) int {
					idx := nextReservationIndex % n
					nextReservationIndex++
					return idx
				})},
			} {
				b.Run(tc.name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						if _, err := tc.s.PickWorker(context.Background(), function, states, demand); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func benchmarkWorkers(n int) []*core.WorkerState {
	workers := make([]*core.WorkerState, n)
	for i := range workers {
		workers[i] = &core.WorkerState{
			WorkerId:    uint64(i + 1),
			Healthy:     true,
			Schedulable: true,
			Capacity:    &core.ResourceSpec{CpuUnits: 2000, MemoryBytes: 8 << 30, DiskBytes: 50 << 30},
			Allocated:   &core.ResourceUsage{CpuUnits: uint64(i%20) * 100, MemoryBytes: uint64(i%20) * (128 << 20), DiskBytes: uint64(i%20) * (64 << 20)},
			Instances:   uint64(i % 20),
		}
	}
	return workers
}
