package dst

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTImageAwarePrefersCachedWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	if len(h.Cfg.WorkerGRPCs) < 3 {
		t.Skipf("need >=3 workers for image-aware affinity, got %d", len(h.Cfg.WorkerGRPCs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Image-aware placement is leaf-local, so pin the function to one leaf with
	// consistent-hashing routing; otherwise scale-out lands on other leaves with
	// empty caches and the affinity assertion is meaningless. Both policies come
	// from the config document, not YAML or a per-function field.
	h.ApplyPlatformConfig(ctx, shared.ConsistentHashingRouting(), shared.ImageAwarePlacement())

	// sleep-http keeps requests in-flight so MaxConcurrency=1 forces real scale-out.
	image := shared.SleepHTTPImage

	user, err := h.CP.CreateUser(ctx, "dst-image-aware")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), image, "http", h.ScaleToZeroIdle, 1, 8,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fn.GetFunctionId()) })

	if body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fn.GetFunctionId(), []byte("warm")); err != nil || status != http.StatusOK {
		t.Fatalf("warm invoke: status=%d err=%v body=%q", status, err, body)
	}
	time.Sleep(500 * time.Millisecond)

	cachedWorkers := map[string]bool{}
	for _, addr := range h.Cfg.WorkerGRPCs {
		client, err := shared.NewWorkerClientAt(addr)
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		callCtx, callCancel := context.WithTimeout(ctx, 3*time.Second)
		state, err := client.CurrentState(callCtx)
		callCancel()
		_ = client.Close()
		if err != nil {
			t.Fatalf("CurrentState %s: %v", addr, err)
		}
		for _, img := range state.GetCachedImages() {
			if img != nil && (img.GetImage() == image || strings.HasPrefix(img.GetImage(), image)) {
				cachedWorkers[addr] = true
			}
		}
	}
	if len(cachedWorkers) == 0 {
		t.Fatal("expected warm invoke to populate cached_images on at least one worker")
	}

	const burst = 8
	var wg sync.WaitGroup
	errs := make(chan error, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fn.GetFunctionId(), []byte("burst"))
			if err != nil {
				errs <- err
				return
			}
			if status != http.StatusOK {
				errs <- fmt.Errorf("invoke %d status=%d body=%q", i, status, body)
			}
		}(i)
	}
	// Sample while requests are still sleeping so scale-out instances are visible.
	deadline := time.Now().Add(3 * time.Second)
	var counts map[string]int
	var total int
	for time.Now().Before(deadline) {
		var err error
		counts, err = shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fn.GetFunctionId())
		if err != nil {
			t.Fatal(err)
		}
		total = 0
		for _, n := range counts {
			total += n
		}
		if total >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("burst invoke: %v", err)
		}
	}

	var onCached, onMiss int
	for addr, n := range counts {
		if cachedWorkers[addr] {
			onCached += n
		} else if n > 0 {
			onMiss += n
		}
	}
	if total < 2 {
		t.Fatalf("expected multiple instances under MaxConcurrency=1 sleep burst, got total=%d counts=%v", total, counts)
	}
	if onCached < onMiss {
		t.Fatalf("image-aware did not prefer cached workers: onCached=%d onMiss=%d counts=%v cached=%v", onCached, onMiss, counts, cachedWorkers)
	}
}
