package dst

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/test/shared"
)

const (
	dstInstanceIDLabel = "hyperfaas.instance_id"
	dstFunctionIDLabel = "hyperfaas.function_id"
)

// TestDSTExternallyKilledSandboxIsReplaced verifies the complete lifecycle
// path from Docker through the worker state stream to the leaf. An instance
// that exits outside StopSandbox must disappear from leaf admission before a
// later invocation is routed to a replacement.
func TestDSTExternallyKilledSandboxIsReplaced(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live Docker cluster")
	}
	if os.Getenv("HYPERFAAS_FAKE_CLUSTER") != "" {
		t.Skip("requires Docker worker runtime")
	}

	h := shared.NewHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })
	if _, err := dockerClient.Ping(ctx); err != nil {
		t.Fatalf("Docker daemon is unavailable: %v", err)
	}

	user, err := h.CP.CreateUser(ctx, "dst-killed-sandbox-replacement")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = h.CP.DeleteUser(cleanupCtx, user.GetUserId())
	})

	spec := shared.EchoFunctionWithScale(
		user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle, 1, 1,
	)
	fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), spec)
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = h.CP.DeleteFunction(cleanupCtx, user.GetUserId(), fn.GetFunctionId())
	})

	payload := []byte("before-external-kill")
	// Function registration is eventually consistent across controlplane, leaf,
	// and ingress; wait for the first successful invoke instead of racing it.
	if err := shared.WaitForInvoke(ctx, h.Cfg, user.GetUserId(), fn.GetFunctionId(), payload, 30*time.Second); err != nil {
		t.Fatalf("initial invoke: %v", err)
	}

	initial, err := waitForReadyFunctionInstances(ctx, h.Cfg.WorkerGRPCs, fn.GetFunctionId(), func(instances []*core.InstanceState) bool {
		return len(instances) == 1
	})
	if err != nil {
		t.Fatalf("find initial sandbox: %v", err)
	}
	oldInstanceID := initial[0].GetInstanceId()
	if oldInstanceID == 0 {
		t.Fatal("initial sandbox has zero instance ID")
	}

	containerID, err := findDockerContainer(ctx, dockerClient, fn.GetFunctionId(), oldInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = dockerClient.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{Force: true})
	})
	if err := dockerClient.ContainerKill(ctx, containerID, "SIGKILL"); err != nil {
		t.Fatalf("kill Docker container %s for instance %d: %v", containerID[:12], oldInstanceID, err)
	}

	if _, err := waitForReadyFunctionInstances(ctx, h.Cfg.WorkerGRPCs, fn.GetFunctionId(), func(instances []*core.InstanceState) bool {
		return !containsInstanceID(instances, oldInstanceID)
	}); err != nil {
		t.Fatalf("worker did not remove killed instance %d: %v", oldInstanceID, err)
	}

	// Worker updates are full snapshots. Wait until the leaf's advertised ready
	// count agrees with the worker view, proving that the old address has also
	// left leaf admission. The leaf may already have started a replacement.
	if err := waitForLeafWorkerAgreement(ctx, h, fn.GetFunctionId(), oldInstanceID); err != nil {
		t.Fatalf("killed instance did not propagate to leaf: %v", err)
	}

	payload = []byte("after-external-kill")
	if err := shared.WaitForInvoke(ctx, h.Cfg, user.GetUserId(), fn.GetFunctionId(), payload, 45*time.Second); err != nil {
		t.Fatalf("invoke after kill: %v", err)
	}

	replacement, err := waitForReadyFunctionInstances(ctx, h.Cfg.WorkerGRPCs, fn.GetFunctionId(), func(instances []*core.InstanceState) bool {
		for _, instance := range instances {
			if instance.GetInstanceId() != 0 && instance.GetInstanceId() != oldInstanceID {
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("find replacement for killed instance %d: %v", oldInstanceID, err)
	}
	for _, instance := range replacement {
		if instance.GetInstanceId() != oldInstanceID {
			t.Logf("killed instance %d was replaced by instance %d", oldInstanceID, instance.GetInstanceId())
			return
		}
	}
	t.Fatalf("replacement still used killed instance ID %d", oldInstanceID)
}

func waitForReadyFunctionInstances(ctx context.Context, workerAddrs []string, functionID uint64, ready func([]*core.InstanceState) bool) ([]*core.InstanceState, error) {
	if len(workerAddrs) == 0 {
		return nil, fmt.Errorf("no workers configured")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	ids := map[uint64]struct{}{functionID: {}}
	var last []*core.InstanceState
	var lastErr error
	for {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		last, lastErr = shared.RunningInstancesAcrossWorkers(callCtx, workerAddrs, ids)
		cancel()
		if lastErr == nil && ready(last) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("timeout: %w (last instances: %s, last error: %v)", ctx.Err(), shared.FormatRunning(last), lastErr)
		case <-ticker.C:
		}
	}
}

func waitForLeafWorkerAgreement(ctx context.Context, h *shared.Harness, functionID, oldInstanceID uint64) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastWorkerCount int
	var lastLeafCount uint32
	var lastErr error
	for {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		instances, workerErr := shared.RunningInstancesAcrossWorkers(callCtx, h.Cfg.WorkerGRPCs, map[uint64]struct{}{functionID: {}})
		cancel()
		if workerErr == nil && !containsInstanceID(instances, oldInstanceID) {
			callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
			state, leafErr := leafCurrentState(callCtx, h.Cfg.LeafGRPC)
			cancel()
			if leafErr == nil {
				lastWorkerCount = len(instances)
				lastLeafCount = readyInstancesForFunction(state, functionID)
				if lastLeafCount == uint32(lastWorkerCount) {
					return nil
				}
				lastErr = nil
			} else {
				lastErr = leafErr
			}
		} else {
			lastErr = workerErr
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout: %w (worker ready=%d, leaf ready=%d, last error=%v)", ctx.Err(), lastWorkerCount, lastLeafCount, lastErr)
		case <-ticker.C:
		}
	}
}

func readyInstancesForFunction(state *core.LeafState, functionID uint64) uint32 {
	for _, capacity := range state.GetFunctions() {
		if capacity.GetFunctionId() == functionID {
			return capacity.GetReadyInstances()
		}
	}
	return 0
}

func containsInstanceID(instances []*core.InstanceState, instanceID uint64) bool {
	for _, instance := range instances {
		if instance.GetInstanceId() == instanceID {
			return true
		}
	}
	return false
}

func findDockerContainer(ctx context.Context, dockerClient *client.Client, functionID, instanceID uint64) (string, error) {
	args := filters.NewArgs(
		filters.Arg("label", fmt.Sprintf("%s=%d", dstFunctionIDLabel, functionID)),
		filters.Arg("label", fmt.Sprintf("%s=%d", dstInstanceIDLabel, instanceID)),
	)
	containers, err := dockerClient.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", fmt.Errorf("list Docker containers for instance %d: %w", instanceID, err)
	}
	if len(containers) != 1 {
		return "", fmt.Errorf("found %d Docker containers for function %d instance %d, want 1", len(containers), functionID, instanceID)
	}
	return containers[0].ID, nil
}
