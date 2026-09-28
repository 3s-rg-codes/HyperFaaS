package scheduler

import (
	"context"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

func TestResourceAwarePicksLeastLoadedWorker(t *testing.T) {
	s := NewResourceAware(4)
	decision, err := s.PickWorker(context.Background(), functionWithResources(100, 128), []*core.WorkerState{
		workerState(1, 1000, 1024, 900, 128),
		workerState(2, 1000, 1024, 100, 128),
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 2 {
		t.Fatalf("expected worker 2, got %d", decision.GetWorkerId())
	}
}

func TestResourceAwarePlacesDespiteDeclaredResourceShortfall(t *testing.T) {
	s := NewResourceAware(4)
	decision, err := s.PickWorker(context.Background(), functionWithResources(200, 900), []*core.WorkerState{
		workerState(1, 1000, 1024, 900, 128),
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 1 {
		t.Fatalf("expected worker 1 despite declared resource shortfall, got %d", decision.GetWorkerId())
	}
}

func TestReservationAwarePlacesDespiteDeclaredResourceShortfall(t *testing.T) {
	s := NewReservationAware(4)
	decision, err := s.PickWorker(context.Background(), functionWithResources(200, 900), []*core.WorkerState{
		workerState(1, 1000, 1024, 900, 128),
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 1 {
		t.Fatalf("expected worker 1 despite declared resource shortfall, got %d", decision.GetWorkerId())
	}
}

func TestColdStartAwareUnlimitedInstancesPerWorker(t *testing.T) {
	s := NewColdStartAware(0)
	decision, err := s.PickWorker(context.Background(), functionWithResources(100, 128), []*core.WorkerState{
		{WorkerId: 1, Schedulable: true, Healthy: true, Instances: 10_000},
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 1 {
		t.Fatalf("expected worker 1 with unlimited cap, got %d", decision.GetWorkerId())
	}
}

func TestColdStartAwareIgnoresDeclaredResourceCapacity(t *testing.T) {
	s := NewColdStartAware(4)
	decision, err := s.PickWorker(context.Background(), functionWithResources(100, 128), []*core.WorkerState{
		workerState(1, 2000, 4096, 2000, 4096),
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 1 {
		t.Fatalf("expected worker 1 despite full declared CPU budget, got %d", decision.GetWorkerId())
	}
}

func functionWithResources(cpu uint64, memoryMiB uint64) *core.FunctionSpec {
	return &core.FunctionSpec{Runtime: &core.RuntimeSpec{Resources: &core.ResourceSpec{CpuUnits: cpu, MemoryBytes: memoryMiB * 1024 * 1024}}}
}

func workerState(id, cpu, memoryMiB, usedCPU, usedMemoryMiB uint64) *core.WorkerState {
	return &core.WorkerState{
		WorkerId:    id,
		Schedulable: true,
		Healthy:     true,
		Capacity:    &core.ResourceSpec{CpuUnits: cpu, MemoryBytes: memoryMiB * 1024 * 1024},
		Allocated:   &core.ResourceUsage{CpuUnits: usedCPU, MemoryBytes: usedMemoryMiB * 1024 * 1024},
	}
}
