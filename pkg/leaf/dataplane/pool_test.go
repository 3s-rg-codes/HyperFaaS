package dataplane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSandboxPoolUsesFiniteDefault(t *testing.T) {
	pool := NewSandboxPool(0, 1)
	pool.Add(Sandbox{InstanceID: 1, Address: "sandbox:1"})

	leases := make([]Lease, 0, DefaultSandboxConcurrency)
	for range DefaultSandboxConcurrency {
		lease, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	snapshot := pool.Snapshot()
	if snapshot.AvailableConcurrency != 0 || snapshot.Executing != DefaultSandboxConcurrency {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire beyond default limit: %v", err)
	}
	for _, lease := range leases {
		lease.Release()
	}
}

func TestSandboxPoolCapacityAndExactRelease(t *testing.T) {
	pool := NewSandboxPool(2, 2)
	pool.Add(Sandbox{InstanceID: 1, WorkerID: 3, Address: "sandbox:1", Protocol: "http"})

	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := lease.Sandbox(); got.InstanceID != 1 || got.WorkerID != 3 || got.Address != "sandbox:1" {
		t.Fatalf("sandbox = %+v", got)
	}
	before := pool.Snapshot()
	if before.ReadyInstances != 1 || before.AvailableConcurrency != 1 || before.Executing != 1 {
		t.Fatalf("before release = %+v", before)
	}
	lease.Release()
	lease.Release()
	after := pool.Snapshot()
	if after.AvailableConcurrency != 2 || after.Executing != 0 || after.Completed != 1 {
		t.Fatalf("after release = %+v", after)
	}
}

func TestSandboxPoolConcurrentDoubleRelease(t *testing.T) {
	pool := NewSandboxPool(2, 2)
	pool.Add(Sandbox{InstanceID: 1, Address: "sandbox:1"})

	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease.Release()
		}()
	}
	wg.Wait()

	after := pool.Snapshot()
	if after.Executing != 0 || after.Completed != 1 || after.AvailableConcurrency != 2 {
		t.Fatalf("after concurrent release = %+v", after)
	}
}

func TestSandboxPoolQueueIsBoundedFIFO(t *testing.T) {
	pool := NewSandboxPool(1, 2)
	pool.Add(Sandbox{InstanceID: 1, Address: "sandbox:1"})
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		order int
		lease Lease
		err   error
	}
	results := make(chan result, 2)
	for order := 1; order <= 2; order++ {
		go func() {
			lease, err := pool.Acquire(context.Background())
			results <- result{order: order, lease: lease, err: err}
		}()
		waitForQueued(t, pool, order)
	}
	if _, err := pool.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Acquire beyond queue = %v", err)
	}

	held.Release()
	first := <-results
	if first.err != nil || first.order != 1 {
		t.Fatalf("first result = %+v", first)
	}
	first.lease.Release()
	second := <-results
	if second.err != nil || second.order != 2 {
		t.Fatalf("second result = %+v", second)
	}
	second.lease.Release()
}

func TestSandboxPoolDrainWaitsForLease(t *testing.T) {
	pool := NewSandboxPool(1, 1)
	pool.Add(Sandbox{InstanceID: 7, Address: "sandbox:7"})
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan func(bool), 1)
	go func() {
		finish, err := pool.Drain(context.Background(), 7)
		if err != nil {
			t.Errorf("Drain: %v", err)
			return
		}
		done <- finish
	}()
	waitForReady(t, pool, 0)
	select {
	case <-done:
		t.Fatal("drain completed before release")
	default:
	}
	lease.Release()
	select {
	case finish := <-done:
		finish(true)
	case <-time.After(time.Second):
		t.Fatal("drain did not complete")
	}
	if snapshot := pool.Snapshot(); snapshot.ReadyInstances != 0 {
		t.Fatalf("snapshot after drain = %+v", snapshot)
	}
}

func waitForQueued(t *testing.T, pool *SandboxPool, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pool.Snapshot().Queued == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queued = %d, want %d", pool.Snapshot().Queued, want)
}

func waitForReady(t *testing.T, pool *SandboxPool, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pool.Snapshot().ReadyInstances == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("ready = %d, want %d", pool.Snapshot().ReadyInstances, want)
}
