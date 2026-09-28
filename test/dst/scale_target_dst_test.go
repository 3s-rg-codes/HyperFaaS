package dst

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTSoftTargetScalesOutWhenBreakerUnlimited(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	user, err := h.CP.CreateUser(ctx, "dst-soft-target")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	spec := shared.EchoFunctionWithScale(
		user.GetUserId(), shared.SleepHTTPImage, "http", 5*time.Minute, 0, 16,
	)
	spec.Scale.TargetConcurrency = 5
	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), spec)
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	fnID := fn.GetFunctionId()
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fnID) })
	waitHTTPOK(t, ctx, h, user.GetUserId(), fnID, []byte("warm"))

	hold, holdCancel := context.WithTimeout(ctx, 8*time.Second)
	defer holdCancel()

	const inflight = 20
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < inflight; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for hold.Err() == nil {
				body, status, err := shared.InvokeHTTP(hold, h.Cfg.IngressHTTP, user.GetUserId(), fnID, []byte("hold"))
				if err != nil {
					if hold.Err() != nil {
						return
					}
					failures.Add(1)
					return
				}
				if status != http.StatusOK {
					failures.Add(1)
					t.Logf("invoke status=%d body=%q", status, body)
					return
				}
			}
		}()
	}

	deadline := time.Now().Add(8 * time.Second)
	var peak int
	var counts map[string]int
	for time.Now().Before(deadline) {
		counts, err = shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fnID)
		if err != nil {
			holdCancel()
			wg.Wait()
			t.Fatal(err)
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total > peak {
			peak = total
		}
		if peak >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	holdCancel()
	wg.Wait()

	if peak < 2 {
		t.Fatalf("soft target 5 with ~20 in-flight sleep should scale out, peak=%d counts=%v", peak, counts)
	}
	if failures.Load() > 4 {
		t.Fatalf("too many invoke failures during hold: %d", failures.Load())
	}
}

func TestDSTUnlimitedBreakerKeepsOneReplicaUnderLightEcho(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	user, err := h.CP.CreateUser(ctx, "dst-unlimited-breaker")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	spec := shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", 5*time.Minute, 0, 8,
	)
	spec.Scale.TargetConcurrency = 100
	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), spec)
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	fnID := fn.GetFunctionId()
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fnID) })
	waitHTTPOK(t, ctx, h, user.GetUserId(), fnID, []byte("warm"))

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fnID, []byte("echo"))
			if err != nil {
				errs <- err
				return
			}
			if status != http.StatusOK {
				errs <- fmt.Errorf("invoke %d status=%d body=%q", i, status, body)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	// Echo is fast; in-flight never approaches the soft target of 100, so each
	// leaf that saw traffic should keep a single replica (per-leaf autoscaling).
	deadline := time.Now().Add(3 * time.Second)
	var counts map[string]int
	leafIDs := shared.WorkerLeafIDs(h.Cfg)
	for time.Now().Before(deadline) {
		counts, err = shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fnID)
		if err != nil {
			t.Fatal(err)
		}
		byLeaf := map[uint64]int{}
		total := 0
		for i, addr := range h.Cfg.WorkerGRPCs {
			n := counts[addr]
			total += n
			leaf := uint64(0)
			if i < len(leafIDs) {
				leaf = leafIDs[i]
			}
			byLeaf[leaf] += n
		}
		for leaf, n := range byLeaf {
			if n > 1 {
				t.Fatalf("leaf %d has %d instances under light echo (target 100); want at most 1. counts=%v", leaf, n, counts)
			}
		}
		if total < 1 {
			t.Fatal("expected at least one warm replica")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestDSTMaxConcurrencyOneStillScalesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	user, err := h.CP.CreateUser(ctx, "dst-cc1-scale")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), shared.SleepHTTPImage, "http", 5*time.Minute, 1, 8,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	fnID := fn.GetFunctionId()
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fnID) })
	waitHTTPOK(t, ctx, h, user.GetUserId(), fnID, []byte("warm"))

	const burst = 8
	var wg sync.WaitGroup
	invokeErrs := make(chan error, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fnID, []byte("burst"))
			if err != nil {
				invokeErrs <- fmt.Errorf("invoke %d: %w", i, err)
				return
			}
			if status != http.StatusOK {
				invokeErrs <- fmt.Errorf("invoke %d: status=%d body=%q", i, status, body)
				return
			}
			invokeErrs <- nil
		}(i)
	}

	deadline := time.Now().Add(4 * time.Second)
	var peak int
	var counts map[string]int
	for time.Now().Before(deadline) {
		var err error
		counts, err = shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fnID)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total > peak {
			peak = total
		}
		if peak >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wg.Wait()
	close(invokeErrs)
	for err := range invokeErrs {
		if err != nil {
			t.Errorf("burst invoke failed: %v", err)
		}
	}
	if peak < 2 {
		t.Fatalf("max_concurrency=1 sleep burst should still scale out, peak=%d counts=%v", peak, counts)
	}
}

func waitHTTPOK(t *testing.T, ctx context.Context, h *shared.Harness, userID, fnID uint64, body []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastStatus int
	var lastErr error
	var lastBody []byte
	for time.Now().Before(deadline) {
		lastBody, lastStatus, lastErr = shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, userID, fnID, body)
		if lastErr == nil && lastStatus == http.StatusOK {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("warm invoke never succeeded: status=%d err=%v body=%q", lastStatus, lastErr, lastBody)
}
