package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/core"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

// StateProjection selects which optional worker-state fields the leaf asks the
// worker to compute and send. Lifecycle state is always sent. The placement
// controller owns the projection; it changes only when the placement or routing
// policy changes.
type StateProjection struct {
	LoadAverageNorm bool
	CachedImages    bool
}

// Client talks to a worker SandboxService.
type Client struct {
	index   int
	address string
	conn    *grpc.ClientConn
	client  workerpb.SandboxServiceClient
	logger  *slog.Logger

	startTimeout time.Duration
	stopTimeout  time.Duration
	dialTimeout  time.Duration

	eventWatchOnce sync.Once
}

func NewClient(ctx context.Context, index int, address string, dialTimeout, startTimeout, stopTimeout time.Duration, logger *slog.Logger) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("dial worker %s: %w", address, err)
	}
	return &Client{
		index:        index,
		address:      address,
		conn:         conn,
		client:       workerpb.NewSandboxServiceClient(conn),
		logger:       logger,
		startTimeout: startTimeout,
		stopTimeout:  stopTimeout,
		dialTimeout:  dialTimeout,
	}, nil
}

func (c *Client) Index() int      { return c.index }
func (c *Client) Address() string { return c.address }

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) PrepareImage(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	return c.client.PrepareImage(ctx, &workerpb.PrepareImageRequest{Function: function})
}

func (c *Client) CreateSandbox(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()
	functionID := uint64(0)
	if req.GetFunction() != nil {
		functionID = req.GetFunction().GetFunctionId()
	}
	c.logger.Info("start_sandbox_rpc_begin",
		"function_id", functionID,
		"instance_id", req.GetInstanceId(),
		"worker_id", req.GetWorkerId(),
	)
	inst, err := c.client.CreateSandbox(startCtx, &workerpb.CreateSandboxRequest{Request: req})
	c.logger.Info("start_sandbox_rpc_end",
		"function_id", functionID,
		"instance_id", req.GetInstanceId(),
		"worker_id", req.GetWorkerId(),
		"error", err,
	)
	return inst, err
}

func (c *Client) StopSandbox(ctx context.Context, instanceID uint64) error {
	stopCtx, cancel := context.WithTimeout(ctx, c.stopTimeout)
	defer cancel()
	_, err := c.client.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: instanceID})
	return err
}

// ListSandboxes fetches every sandbox with per-sandbox resource usage. This is
// very inefficient: the worker performs one sequential runtime Stats call per
// ready sandbox. It is intended only for tests and debugging, never for a hot
// path. Hot paths use the WatchState relist plus the sandbox-removal events.
func (c *Client) ListSandboxes(ctx context.Context) ([]*core.InstanceState, error) {
	resp, err := c.client.ListSandboxes(ctx, &workerpb.ListSandboxesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetInstances(), nil
}

// CurrentState performs the unary diagnostic worker-state read. It is ungated
// and not used on the placement path; the placement controller uses the
// projection-gated StartWatch stream.
func (c *Client) CurrentState(ctx context.Context) (*core.WorkerState, error) {
	return c.client.CurrentState(ctx, &workerpb.CurrentWorkerStateRequest{})
}

// StartWatch streams projection-gated worker state until ctx is done. It is
// restartable: the placement controller cancels the previous ctx and calls
// StartWatch again with the new projection when the placement or routing policy
// changes, so it must not be called twice with the same ctx.
func (c *Client) StartWatch(ctx context.Context, backoff time.Duration, projection StateProjection, onUpdate func(*core.WorkerState)) {
	go c.runWatch(ctx, backoff, projection, onUpdate)
}

func (c *Client) runWatch(ctx context.Context, backoff time.Duration, projection StateProjection, onUpdate func(*core.WorkerState)) {
	if backoff <= 0 {
		backoff = time.Second
	}
	req := &workerpb.WatchWorkerStateRequest{
		Projection: &workerpb.WorkerStateProjection{
			LoadAverageNorm: projection.LoadAverageNorm,
			CachedImages:    projection.CachedImages,
		},
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		stream, err := c.client.WatchState(ctx, req)
		if err != nil {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			continue
		}
		for {
			state, err := stream.Recv()
			if err != nil {
				break
			}
			if onUpdate != nil {
				onUpdate(state)
			}
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}

// StartEventWatch streams low-latency sandbox removal events until ctx is done.
// It reconnects with the given backoff. onRemoval receives the instance ID and
// the worker-local revision after the removal.
func (c *Client) StartEventWatch(ctx context.Context, backoff time.Duration, onRemoval func(functionID, instanceID, workerRevision uint64)) {
	c.eventWatchOnce.Do(func() {
		go c.runEventWatch(ctx, backoff, onRemoval)
	})
}

func (c *Client) runEventWatch(ctx context.Context, backoff time.Duration, onRemoval func(uint64, uint64, uint64)) {
	if backoff <= 0 {
		backoff = time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		stream, err := c.client.WatchSandboxEvents(ctx, &workerpb.WatchSandboxEventsRequest{})
		if err != nil {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			continue
		}
		for {
			removal, err := stream.Recv()
			if err != nil {
				break
			}
			if onRemoval != nil && removal != nil {
				onRemoval(removal.GetFunctionId(), removal.GetInstanceId(), removal.GetWorkerRevision())
			}
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}
