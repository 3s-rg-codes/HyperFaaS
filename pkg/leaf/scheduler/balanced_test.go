package scheduler

import (
	"context"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

func TestBalancedRoundRobinPicksWorkerWithCapacity(t *testing.T) {
	s := NewBalancedRoundRobin(2, 2)
	decision, err := s.PickWorker(context.Background(), &core.FunctionSpec{}, []*core.WorkerState{
		{WorkerId: 1, Healthy: true, Schedulable: true, Instances: 2},
		{WorkerId: 2, Healthy: true, Schedulable: true, Instances: 0},
	}, &core.ScaleDemand{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetWorkerId() != 2 {
		t.Fatalf("expected worker 2, got %d", decision.GetWorkerId())
	}
}
