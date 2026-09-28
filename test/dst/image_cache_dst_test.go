package dst

import (
	"context"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTCachedImagesPopulateAfterInvoke(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if len(h.Cfg.WorkerGRPCs) == 0 {
		t.Fatal("no workers configured")
	}

	before, err := shared.WorkerHasCachedImage(ctx, h.Cfg.WorkerGRPCs, shared.EchoHTTPImage)
	if err != nil {
		t.Fatalf("precheck cached images: %v", err)
	}

	user, err := h.CP.CreateUser(ctx, "dst-image-cache")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 1, 2,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fn.GetFunctionId()) })

	// Function registration is eventually consistent across controlplane, leaf,
	// and ingress; wait for the first successful invoke instead of racing it.
	if err := shared.WaitForInvoke(ctx, h.Cfg, user.GetUserId(), fn.GetFunctionId(), []byte("cache-me"), 30*time.Second); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		ok, err := shared.WorkerHasCachedImage(ctx, h.Cfg.WorkerGRPCs, shared.EchoHTTPImage)
		if err != nil {
			t.Fatalf("cached images: %v", err)
		}
		if ok {
			if before {
				t.Log("image was already cached before invoke (cluster reused); still present after invoke")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("expected at least one worker CachedImages to include echo-http after invoke")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
