package leafv2

import (
	"sync"
	"testing"
)

func TestBalancedRoundRobinDistributesCalls(t *testing.T) {
	sched := newBalancedRoundRobin(3)
	state := []WorkerState{
		{Index: 0, Instances: 1, InFlight: 0},
		{Index: 1, Instances: 1, InFlight: 0},
		{Index: 2, Instances: 1, InFlight: 0},
	}

	seen := make(map[int]int)
	for i := 0; i < 9; i++ {
		idx := sched.PickForCall(state, 1)
		if idx == -1 {
			t.Fatalf("unexpected no-capacity on iteration %d", i)
		}
		seen[idx]++
	}

	for worker := 0; worker < 3; worker++ {
		if seen[worker] != 3 {
			t.Fatalf("worker %d received %d calls, want 3", worker, seen[worker])
		}
	}
}

func TestBalancedRoundRobinSkipsExhaustedWorkers(t *testing.T) {
	sched := newBalancedRoundRobin(2)
	state := []WorkerState{
		{Index: 0, Instances: 0, InFlight: 0},
		{Index: 1, Instances: 1, InFlight: 0},
	}

	idx := sched.PickForCall(state, 1)
	if idx != 1 {
		t.Fatalf("expected scheduler to skip exhausted worker, got %d", idx)
	}
}

func TestBalancedRoundRobinScaleRespectsMax(t *testing.T) {
	sched := newBalancedRoundRobin(2)
	state := []WorkerState{
		{Index: 0, Instances: 2, InFlight: 0},
		{Index: 1, Instances: 1, InFlight: 0},
	}

	idx, ok := sched.PickForScale(state, 2)
	if !ok {
		t.Fatal("expected to find scale target")
	}
	if idx != 1 {
		t.Fatalf("expected worker 1 to be picked for scale, got %d", idx)
	}
}

func TestBalancedRoundRobinConcurrentCalls(t *testing.T) {
	sched := newBalancedRoundRobin(2)
	state := []WorkerState{
		{Index: 0, Instances: 1, InFlight: 0},
		{Index: 1, Instances: 1, InFlight: 0},
	}

	calls := 10_000
	counts := make([]int, 2)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx := sched.PickForCall(state, 1)
			if idx == -1 {
				t.Error("scheduler returned no capacity")
				return
			}
			mu.Lock()
			counts[idx]++
			mu.Unlock()
		}()
	}

	wg.Wait()

	for worker := range counts {
		if counts[worker] == 0 {
			to := counts[1-worker]
			t.Fatalf("worker %d received no calls while other got %d", worker, to)
		}
	}
}
