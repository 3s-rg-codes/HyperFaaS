package dst

import (
	"context"
	"net/http"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTPrepareImageRPCPopulatesCache(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	if len(h.Cfg.WorkerGRPCs) == 0 {
		t.Fatal("no workers configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	addr := h.Cfg.WorkerGRPCs[0]
	client, err := shared.NewWorkerClientAt(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	fn := &core.FunctionSpec{
		Runtime: &core.RuntimeSpec{
			Image:     shared.EchoHTTPImage,
			Protocol:  "http",
			Isolation: core.IsolationKind_ISOLATION_KIND_FAKE,
		},
	}
	if _, err := client.PrepareImage(ctx, fn); err != nil {
		t.Fatalf("PrepareImage: %v", err)
	}
	state, err := client.CurrentState(ctx)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	found := false
	for _, img := range state.GetCachedImages() {
		if img == nil {
			continue
		}
		if img.GetImage() == shared.EchoHTTPImage || img.GetDigest() == shared.EchoHTTPImage {
			found = true
			if img.GetCachedAt() == nil {
				t.Fatal("CachedAt should be set after PrepareImage")
			}
			break
		}
	}
	if !found {
		t.Fatalf("worker %s did not advertise %s in cached_images after PrepareImage", addr, shared.EchoHTTPImage)
	}
}

func TestDSTScaleToZeroMultiWorkerSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	if len(h.Cfg.WorkerGRPCs) < 2 {
		t.Skipf("need >=2 workers, got %d", len(h.Cfg.WorkerGRPCs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	user, err := h.CP.CreateUser(ctx, "dst-scale-zero-smoke")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 1, 4,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fn.GetFunctionId()) })

	if body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fn.GetFunctionId(), []byte("once")); err != nil || status != http.StatusOK {
		t.Fatalf("invoke: status=%d err=%v body=%q", status, err, body)
	}

	// Idle timeout + leaf reconcile (2s) + slack.
	wait := h.ScaleToZeroIdle + 5*time.Second
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(wait):
	}

	ids := map[uint64]struct{}{fn.GetFunctionId(): {}}
	running, err := shared.RunningInstancesAcrossWorkers(ctx, h.Cfg.WorkerGRPCs, ids)
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	if len(running) > 0 {
		t.Fatalf("expected scale-to-zero, still running: %s", shared.FormatRunning(running))
	}
}
