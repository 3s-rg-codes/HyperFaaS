package shared

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/core"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

type WorkerClient struct {
	conn   *grpc.ClientConn
	client workerpb.SandboxServiceClient
}

func NewWorkerClient(cfg Config) (*WorkerClient, error) {
	return NewWorkerClientAt(cfg.WorkerGRPC)
}

func NewWorkerClientAt(addr string) (*WorkerClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &WorkerClient{conn: conn, client: workerpb.NewSandboxServiceClient(conn)}, nil
}

func (c *WorkerClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// RunningInstancesAcrossWorkers lists ready sandboxes for the given function IDs across all workers.
// Every worker address must respond; unreachable workers are treated as errors so scale-to-zero
// cannot false-pass when a wedged worker still holds instances.
func RunningInstancesAcrossWorkers(ctx context.Context, addrs []string, functionIDs map[uint64]struct{}) ([]*core.InstanceState, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no worker addresses configured")
	}
	var running []*core.InstanceState
	for _, addr := range addrs {
		client, err := NewWorkerClientAt(addr)
		if err != nil {
			return nil, fmt.Errorf("worker %s: %w", addr, err)
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		got, err := client.RunningInstances(callCtx, functionIDs)
		cancel()
		_ = client.Close()
		if err != nil {
			return nil, fmt.Errorf("worker %s: %w", addr, err)
		}
		running = append(running, got...)
	}
	return running, nil
}

func (c *WorkerClient) RunningInstances(ctx context.Context, functionIDs map[uint64]struct{}) ([]*core.InstanceState, error) {
	// CurrentState carries the fast identity snapshot. Do not use ListSandboxes
	// here: it performs one sequential Docker Stats call per sandbox and can
	// exceed the caller's deadline under load.
	state, err := c.client.CurrentState(ctx, &workerpb.CurrentWorkerStateRequest{})
	if err != nil {
		return nil, err
	}
	var running []*core.InstanceState
	for _, inst := range state.GetSandboxStates() {
		if inst == nil || !inst.GetReady() || inst.GetStopping() {
			continue
		}
		if len(functionIDs) > 0 {
			if _, ok := functionIDs[inst.GetFunctionId()]; !ok {
				continue
			}
		}
		running = append(running, inst)
	}
	return running, nil
}

func (c *WorkerClient) CurrentState(ctx context.Context) (*core.WorkerState, error) {
	return c.client.CurrentState(ctx, &workerpb.CurrentWorkerStateRequest{})
}

func (c *WorkerClient) SetLoadOverride(ctx context.Context, load float64, clear bool) error {
	_, err := c.client.SetLoadOverride(ctx, &workerpb.SetLoadOverrideRequest{
		LoadAverageNorm: load,
		Clear:           clear,
	})
	return err
}

// WorkerHasCachedImage reports whether any configured worker advertises image.
func WorkerHasCachedImage(ctx context.Context, addrs []string, image string) (bool, error) {
	if len(addrs) == 0 {
		return false, fmt.Errorf("no worker addresses configured")
	}
	var firstErr error
	okWorkers := 0
	for _, addr := range addrs {
		client, err := NewWorkerClientAt(addr)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("worker %s: %w", addr, err)
			}
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		state, err := client.CurrentState(callCtx)
		cancel()
		_ = client.Close()
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("worker %s CurrentState: %w", addr, err)
			}
			continue
		}
		okWorkers++
		for _, img := range state.GetCachedImages() {
			if img == nil {
				continue
			}
			if img.GetImage() == image || img.GetDigest() == image {
				return true, nil
			}
		}
	}
	if okWorkers == 0 && firstErr != nil {
		return false, firstErr
	}
	return false, nil
}

// CountRunningByWorker returns ready instance counts per worker address for the function.
// Every worker address must respond.
func CountRunningByWorker(ctx context.Context, addrs []string, functionID uint64) (map[string]int, error) {
	out := make(map[string]int, len(addrs))
	ids := map[uint64]struct{}{functionID: {}}
	for _, addr := range addrs {
		client, err := NewWorkerClientAt(addr)
		if err != nil {
			return nil, fmt.Errorf("worker %s: %w", addr, err)
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		got, err := client.RunningInstances(callCtx, ids)
		cancel()
		_ = client.Close()
		if err != nil {
			return nil, fmt.Errorf("worker %s: %w", addr, err)
		}
		out[addr] = len(got)
	}
	return out, nil
}

// PrepareImage asks one worker to prepare the function image and returns its WorkerState afterward.
func (c *WorkerClient) PrepareImage(ctx context.Context, fn *core.FunctionSpec) (*core.PreparedArtifact, error) {
	return c.client.PrepareImage(ctx, &workerpb.PrepareImageRequest{Function: fn})
}

func FormatRunning(instances []*core.InstanceState) string {
	if len(instances) == 0 {
		return "none"
	}
	out := ""
	for i, inst := range instances {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("fn=%d inst=%d addr=%s", inst.GetFunctionId(), inst.GetInstanceId(), inst.GetAddress())
	}
	return out
}
