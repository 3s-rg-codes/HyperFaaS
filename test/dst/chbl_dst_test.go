package dst

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/chbl"
	"hyperfaas-ideal-arch/pkg/core"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTBoundedLoadsForwardsFromHotLeaf(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live cluster")
	}
	h := shared.NewHarness(t)
	leafIDs := shared.WorkerLeafIDs(h.Cfg)
	if len(leafIDs) == 0 {
		t.Skip("HYPERFAAS_FAKE_WORKER_LEAF_IDS not set; restart big fake cluster after script update")
	}
	uniqueLeaves := uniqueSorted(leafIDs)
	if len(uniqueLeaves) < 2 {
		t.Skipf("need >=2 leaves for ingress CH-BL forward, got %d", len(uniqueLeaves))
	}
	if len(h.Cfg.LeafGRPCs) < 2 {
		t.Skip("HYPERFAAS_LEAF_GRPCs not set; restart fake cluster")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Select ingress CH-BL through the config document, not YAML. The leaf also
	// needs the same document to request worker load for its leaf_load signal.
	h.ApplyPlatformConfig(ctx, shared.BoundedLoadsRouting(1.0, 3), shared.BalancedRoundRobinPlacement())

	user, err := h.CP.CreateUser(ctx, "dst-chbl-partition")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = h.CP.DeleteUser(context.Background(), user.GetUserId()) })

	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", 5*time.Minute, 0, 8,
	))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	fnID := fn.GetFunctionId()
	t.Cleanup(func() { _ = h.CP.DeleteFunction(context.Background(), user.GetUserId(), fnID) })

	ring := chbl.NewRing(uniqueLeaves)
	home := ring.Home(fnID)
	// Ingress CH-BL forwards from a hot home to the ring's next node, not to an
	// arbitrary other leaf, so the expected destination must be computed the same
	// way. (With exactly two leaves the two coincide.)
	dest := ring.Next(home)
	if dest == 0 || dest == home {
		t.Fatal("no destination leaf")
	}

	t.Cleanup(func() {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer clearCancel()
		for _, addr := range h.Cfg.WorkerGRPCs {
			client, err := shared.NewWorkerClientAt(addr)
			if err != nil {
				continue
			}
			_ = client.SetLoadOverride(clearCtx, 0, true)
			_ = client.Close()
		}
	})

	for i, addr := range h.Cfg.WorkerGRPCs {
		client, err := shared.NewWorkerClientAt(addr)
		if err != nil {
			t.Fatalf("dial worker %s: %v", addr, err)
		}
		load := 0.1
		if leafIDs[i] == home {
			load = 2.0
			// Only the first worker on the home leaf is hot; max() still marks the leaf full.
			homeAlreadyHot := false
			for j := 0; j < i; j++ {
				if leafIDs[j] == home {
					homeAlreadyHot = true
					break
				}
			}
			if homeAlreadyHot {
				load = 0.1
			}
		}
		if err := client.SetLoadOverride(ctx, load, false); err != nil {
			_ = client.Close()
			t.Fatalf("SetLoadOverride %s: %v", addr, err)
		}
		_ = client.Close()
	}

	homeAddr := leafAddr(h.Cfg, uniqueLeaves, home)
	deadline := time.Now().Add(8 * time.Second)
	var leafLoad float64
	for time.Now().Before(deadline) {
		state, err := leafCurrentState(ctx, homeAddr)
		if err == nil {
			leafLoad = state.GetLeafLoad()
			if leafLoad >= 1.0 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if leafLoad < 1.0 {
		t.Fatalf("home leaf %d leaf_load=%v never crossed bound 1.0; max aggregation did not reach ingress", home, leafLoad)
	}
	// Ingress reads leaf_load from heartbeats (~500ms).
	time.Sleep(time.Second)

	for i := 0; i < 6; i++ {
		body, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), fnID, []byte("chbl"))
		if err != nil || status != http.StatusOK {
			t.Fatalf("invoke i=%d status=%d err=%v body=%q", i, status, err, body)
		}
	}
	time.Sleep(time.Second)

	addrToLeaf := map[string]uint64{}
	for i, addr := range h.Cfg.WorkerGRPCs {
		addrToLeaf[addr] = leafIDs[i]
	}
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
		t.Fatalf("no instances after invokes, counts=%v", counts)
	}
	if leavesSeen[home] > 0 && leavesSeen[dest] == 0 {
		t.Fatalf("ingress CH-BL did not escape hot leaf: home=%d dest=%d leaves=%v counts=%v leaf_load=%v", home, dest, leavesSeen, counts, leafLoad)
	}
	if leavesSeen[dest] == 0 {
		t.Fatalf("expected instances on dest leaf %d after home leaf_load=%v, leaves=%v counts=%v", dest, leafLoad, leavesSeen, counts)
	}
}

func uniqueSorted(ids []uint64) []uint64 {
	seen := map[uint64]struct{}{}
	var out []uint64
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func leafAddr(cfg shared.Config, leafIDs []uint64, want uint64) string {
	for i, id := range leafIDs {
		if id == want && i < len(cfg.LeafGRPCs) {
			return cfg.LeafGRPCs[i]
		}
	}
	if len(cfg.LeafGRPCs) > 0 {
		return cfg.LeafGRPCs[0]
	}
	return cfg.LeafGRPC
}

func leafCurrentState(ctx context.Context, addr string) (*core.LeafState, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return leafpkg.NewLeafControlServiceClient(conn).CurrentState(ctx, &leafpkg.CurrentLeafStateRequest{})
}
