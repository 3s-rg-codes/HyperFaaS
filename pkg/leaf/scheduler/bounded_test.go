package scheduler

import (
	"context"
	"testing"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
)

func testWorker(id uint64, load float64) *core.WorkerState {
	return &core.WorkerState{
		WorkerId:        id,
		Healthy:         true,
		Schedulable:     true,
		LoadAverageNorm: load,
	}
}

func TestBoundedLoadsPlacementStaysHomeWhenUnderBound(t *testing.T) {
	s := NewBoundedLoads(3, 0, 1.0, 3)
	workers := []*core.WorkerState{
		testWorker(1, 0.1),
		testWorker(2, 0.1),
		testWorker(3, 0.1),
	}
	fn := &core.FunctionSpec{FunctionId: 10}
	d, err := s.PickWorker(context.Background(), fn, workers, nil)
	if err != nil {
		t.Fatal(err)
	}
	home := chbl.NewRing([]uint64{1, 2, 3}).Home(10)
	if d.GetWorkerId() != home {
		t.Fatalf("home=%d got=%d", home, d.GetWorkerId())
	}
	if d.GetReason() != chbl.ReasonHome {
		t.Fatalf("reason=%q", d.GetReason())
	}
}

func TestBoundedLoadsPlacementForwardsWhenHomeOverBound(t *testing.T) {
	s := NewBoundedLoads(3, 0, 1.0, 3)
	ring := chbl.NewRing([]uint64{1, 2, 3})
	home := ring.Home(10)
	next := ring.Next(home)
	workers := []*core.WorkerState{
		testWorker(1, 0.1),
		testWorker(2, 0.1),
		testWorker(3, 0.1),
	}
	for _, w := range workers {
		if w.GetWorkerId() == home {
			w.LoadAverageNorm = 2.0
		}
	}
	d, err := s.PickWorker(context.Background(), &core.FunctionSpec{FunctionId: 10}, workers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.GetWorkerId() != next {
		t.Fatalf("expected next=%d home=%d got=%d reason=%q", next, home, d.GetWorkerId(), d.GetReason())
	}
	if d.GetReason() != chbl.ReasonForwarded {
		t.Fatalf("reason=%q", d.GetReason())
	}
}

func TestBoundedLoadsPlacementLeastLoadedAfterChain(t *testing.T) {
	s := NewBoundedLoads(3, 0, 1.0, 3)
	workers := []*core.WorkerState{
		testWorker(1, 2.0),
		testWorker(2, 1.5),
		testWorker(3, 3.0),
	}
	d, err := s.PickWorker(context.Background(), &core.FunctionSpec{FunctionId: 10}, workers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.GetReason() != chbl.ReasonLeastLoaded {
		t.Fatalf("reason=%q", d.GetReason())
	}
	if d.GetWorkerId() != 2 {
		t.Fatalf("expected worker 2, got %d", d.GetWorkerId())
	}
}
