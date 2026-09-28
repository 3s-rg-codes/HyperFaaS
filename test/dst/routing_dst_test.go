package dst

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTConsistentHashingLeafStickiness(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	leafIDs := shared.WorkerLeafIDs(h.Cfg)
	if len(leafIDs) == 0 {
		t.Skip("HYPERFAAS_FAKE_WORKER_LEAF_IDS not set; restart big fake cluster after script update")
	}
	uniqueLeaves := map[uint64]struct{}{}
	for _, id := range leafIDs {
		uniqueLeaves[id] = struct{}{}
	}
	if len(uniqueLeaves) < 2 {
		t.Skipf("need >=2 leaves for stickiness, got %d", len(uniqueLeaves))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Select the policy through the config document, not YAML.
	h.ApplyPlatformConfig(ctx, shared.ConsistentHashingRouting(), shared.BalancedRoundRobinPlacement())

	user, err := h.CP.CreateUser(ctx, "dst-routing-sticky")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	const fnCount = 4
	fnIDs := make([]uint64, 0, fnCount)
	for i := 0; i < fnCount; i++ {
		fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
			user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 0, 4,
		))
		if err != nil {
			t.Fatalf("create function %d: %v", i, err)
		}
		id := fn.GetFunctionId()
		fnIDs = append(fnIDs, id)
		t.Cleanup(func() {
			_ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), id)
		})
	}

	for _, fnID := range fnIDs {
		for i := 0; i < 6; i++ {
			body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fnID, []byte("sticky"))
			if err != nil || status != http.StatusOK {
				t.Fatalf("invoke fn=%d i=%d status=%d err=%v body=%q", fnID, i, status, err, body)
			}
		}
	}
	time.Sleep(time.Second)

	addrToLeaf := map[string]uint64{}
	for i, addr := range h.Cfg.WorkerGRPCs {
		addrToLeaf[addr] = leafIDs[i]
	}

	for _, fnID := range fnIDs {
		counts, err := shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fnID)
		if err != nil {
			t.Fatal(err)
		}
		leavesSeen := map[uint64]int{}
		total := 0
		for addr, n := range counts {
			if n == 0 {
				continue
			}
			total += n
			leavesSeen[addrToLeaf[addr]] += n
		}
		if total == 0 {
			t.Fatalf("fn %d has no ready instances after warm invokes", fnID)
		}
		if len(leavesSeen) != 1 {
			t.Fatalf("fn %d expected single-leaf stickiness under consistent-hashing, leaves=%v counts=%v", fnID, leavesSeen, counts)
		}
	}
}

func TestDSTConsistentHashingCrossFunctionSpread(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	leafIDs := shared.WorkerLeafIDs(h.Cfg)
	uniqueLeaves := map[uint64]struct{}{}
	for _, id := range leafIDs {
		uniqueLeaves[id] = struct{}{}
	}
	if len(uniqueLeaves) < 2 {
		t.Skipf("need >=2 leaves, got %d", len(uniqueLeaves))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	h.ApplyPlatformConfig(ctx, shared.ConsistentHashingRouting(), shared.BalancedRoundRobinPlacement())

	user, err := h.CP.CreateUser(ctx, "dst-routing-spread-fns")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	const fnCount = 12
	fnIDs := make([]uint64, 0, fnCount)
	for i := 0; i < fnCount; i++ {
		fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
			user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 0, 2,
		))
		if err != nil {
			t.Fatalf("create function %d: %v", i, err)
		}
		id := fn.GetFunctionId()
		fnIDs = append(fnIDs, id)
		t.Cleanup(func() {
			_ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), id)
		})
		if body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), id, []byte("home")); err != nil || status != http.StatusOK {
			t.Fatalf("invoke fn=%d: status=%d err=%v body=%q", id, status, err, body)
		}
	}
	time.Sleep(time.Second)

	addrToLeaf := map[string]uint64{}
	for i, addr := range h.Cfg.WorkerGRPCs {
		addrToLeaf[addr] = leafIDs[i]
	}
	homes := map[uint64]struct{}{}
	for _, fnID := range fnIDs {
		counts, err := shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fnID)
		if err != nil {
			t.Fatal(err)
		}
		for addr, n := range counts {
			if n == 0 {
				continue
			}
			homes[addrToLeaf[addr]] = struct{}{}
		}
	}
	if len(homes) < 2 {
		t.Fatalf("expected sticky router to spread different functions across leaves, homes=%v", homes)
	}
}

func TestDSTMultiLeafScaleOutSpreads(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	leafIDs := shared.WorkerLeafIDs(h.Cfg)
	if len(leafIDs) == 0 {
		t.Skip("HYPERFAAS_FAKE_WORKER_LEAF_IDS not set; restart big fake cluster after script update")
	}
	uniqueLeaves := map[uint64]struct{}{}
	for _, id := range leafIDs {
		uniqueLeaves[id] = struct{}{}
	}
	if len(uniqueLeaves) < 2 {
		t.Skipf("need >=2 leaves, got %d", len(uniqueLeaves))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Scatter routing: warm-aware leaf selection spreads a cold burst.
	h.ApplyPlatformConfig(ctx, shared.AvailableCapacityRouting(), shared.BalancedRoundRobinPlacement())

	user, err := h.CP.CreateUser(ctx, "dst-routing-spread")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 1, 32,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fn.GetFunctionId()) })

	const burst = 24
	errCh := make(chan error, burst)
	for i := 0; i < burst; i++ {
		go func() {
			body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fn.GetFunctionId(), []byte("spread"))
			if err != nil {
				errCh <- err
				return
			}
			if status != http.StatusOK {
				errCh <- fmt.Errorf("status=%d body=%q", status, body)
				return
			}
			errCh <- nil
		}()
	}
	for i := 0; i < burst; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("burst invoke: %v", err)
		}
	}
	time.Sleep(time.Second)

	addrToLeaf := map[string]uint64{}
	for i, addr := range h.Cfg.WorkerGRPCs {
		addrToLeaf[addr] = leafIDs[i]
	}
	counts, err := shared.CountRunningByWorker(ctx, h.Cfg.WorkerGRPCs, fn.GetFunctionId())
	if err != nil {
		t.Fatal(err)
	}
	leavesSeen := map[uint64]int{}
	total := 0
	for addr, n := range counts {
		if n == 0 {
			continue
		}
		total += n
		leavesSeen[addrToLeaf[addr]] += n
	}
	if total < 2 {
		t.Fatalf("expected multiple instances, got total=%d counts=%v", total, counts)
	}
	if len(leavesSeen) < 2 {
		t.Fatalf("expected multi-leaf spread under available-capacity, leaves=%v counts=%v", leavesSeen, counts)
	}
}
