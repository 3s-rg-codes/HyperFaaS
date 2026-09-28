package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf/autoscaling"
	"hyperfaas-ideal-arch/pkg/leaf/scheduler"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
)

// errPlacementUnavailable is returned while no placement policy is active,
// which happens before the first platform-config document arrives and between
// clearing the old policy and installing the new one during a reload.
var errPlacementUnavailable = status.Error(codes.Unavailable, "placement unavailable")

// PlacementController owns the leaf's active placement policy, the worker state
// streams that feed it, and the scheduler every function actuator shares.
//
// It mirrors ingress RoutingController one tier down: leaf-to-worker placement
// is gated by PlacementNeeds the same way ingress-to-leaf routing is gated by
// RoutingNeeds. On reload it stops accepting placement work, restarts the
// worker streams with the new projection, drops the old telemetry, and installs
// the new scheduler. Placement work is rejected while the controller is not
// serving. This is acceptable because policy configuration changes only while
// no invocation load is in flight; see
// docs/DYNAMIC_POLICY_STATE_GATING_DESIGN.md.
//
// The controller is also the stable scheduler facade: actuators hold it in
// ActuatorConfig.Scheduler and PlacementState unwraps Active() to the concrete
// scheduler for the active policy.
type PlacementController struct {
	logger    *slog.Logger
	workers   []placementWorker
	placement *PlacementState
	backoff   time.Duration
	baseline  time.Duration
	// onState publishes one worker's gated telemetry into the leaf cache.
	onState func(index int, state *core.WorkerState)
	// onReset drops the previous policy's telemetry before the new streams
	// start.
	onReset func()
	// maxInstancesPerWorker is the leaf-local per-worker sandbox cap passed to
	// the scheduler constructors.
	maxInstancesPerWorker int

	mu        sync.Mutex
	scheduler scheduler.PlacementScheduler
	version   uint64
	serving   bool
	cancels   []context.CancelFunc
}

// placementWorker is the subset of leafworker.Client the controller needs. It
// is an interface so tests can drive the controller without a gRPC server.
type placementWorker interface {
	Index() int
	StartWatch(ctx context.Context, backoff time.Duration, projection leafworker.StateProjection, onUpdate func(*core.WorkerState))
}

// NewPlacementController wires a controller. onState and onReset are called
// from the worker stream goroutines and must be cheap and non-blocking.
func NewPlacementController(
	workers []placementWorker,
	placement *PlacementState,
	backoff time.Duration,
	maxInstancesPerWorker uint32,
	baseline time.Duration,
	logger *slog.Logger,
	onState func(index int, state *core.WorkerState),
	onReset func(),
) *PlacementController {
	if backoff <= 0 {
		backoff = time.Second
	}
	if baseline <= 0 {
		baseline = 2 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PlacementController{
		logger:                logger,
		workers:               workers,
		placement:             placement,
		backoff:               backoff,
		baseline:              baseline,
		onState:               onState,
		onReset:               onReset,
		maxInstancesPerWorker: int(maxInstancesPerWorker),
	}
}

// Active returns the concrete scheduler for the active policy, or nil while a
// reload is in progress or before the first policy is installed.
func (c *PlacementController) Active() autoscaling.PlacementScheduler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.serving {
		return nil
	}
	return c.scheduler
}

// PickWorker makes the controller the stable scheduler facade held by every
// actuator. It delegates to the active scheduler. PlacementState normally
// unwraps Active() itself, so this path is used only by callers that hold the
// controller directly.
func (c *PlacementController) PickWorker(ctx context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error) {
	active := c.Active()
	if active == nil {
		return nil, errPlacementUnavailable
	}
	return active.PickWorker(ctx, function, workers, demand)
}

// ApplyConfig installs a placement policy and restarts the worker state streams
// with its exact projection.
//
// The reload sequence follows docs/DYNAMIC_POLICY_STATE_GATING_DESIGN.md:
// placement work is rejected from the moment serving is cleared until the new
// scheduler is installed, and the old telemetry is dropped rather than merged.
// There is no fallback to the previous policy.
func (c *PlacementController) ApplyConfig(ctx context.Context, placement *core.PlacementPolicyConfig, routing *core.RoutingPolicyConfig, version uint64) error {
	sched, err := scheduler.NewFromConfig(placement, len(c.workers), c.maxInstancesPerWorker)
	if err != nil {
		return err
	}
	needs, err := scheduler.NeedsFor(placement)
	if err != nil {
		return err
	}

	// 1. Stop accepting placement work.
	c.mu.Lock()
	c.serving = false
	// 2-5. Stop old streams, drop the old scheduler and telemetry.
	c.stopStreamsLocked()
	c.scheduler = nil
	c.version = version
	c.mu.Unlock()
	if c.onReset != nil {
		c.onReset()
	}

	// The worker projection is the union of the placement signals and the
	// worker signals the leaf's routing production needs. leaf_load is
	// max(worker.load_average_norm), so load-aware ingress routing needs
	// worker load even when the placement policy does not. Aggregate in-flight
	// and per-function capacity are leaf-local and need no worker projection.
	projection := leafworker.StateProjection{
		LoadAverageNorm: needs.Has(scheduler.NeedWorkerLoad) || routingNeedsLeafLoad(routing),
		CachedImages:    needs.Has(scheduler.NeedWorkerImages),
	}
	// Only image-aware placement reads leaf-local image state.
	c.placement.SetTrackImages(needs.Has(scheduler.NeedWorkerImages))
	c.logger.Debug("applying placement policy",
		"policy", scheduler.PlacementPolicyLabel(placement),
		"config_version", version,
		"request_load_average_norm", projection.LoadAverageNorm,
		"request_cached_images", projection.CachedImages,
	)

	// 6. Start new streams with the exact projection and collect a baseline from
	// every worker. The first frame of a stream is always a full snapshot, so no
	// separate snapshot request is needed. A worker that is unreachable must not
	// block the reload: the wait is bounded and the stream retries in the
	// background.
	baselines := make(chan struct{}, len(c.workers))
	cancels := make([]context.CancelFunc, 0, len(c.workers))
	for _, w := range c.workers {
		workerCtx, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		var once sync.Once
		signal := func() {
			once.Do(func() {
				select {
				case baselines <- struct{}{}:
				default:
				}
			})
		}
		worker := w
		worker.StartWatch(workerCtx, c.backoff, projection, func(state *core.WorkerState) {
			if c.onState != nil {
				c.onState(worker.Index(), state)
			}
			signal()
		})
	}

	remaining := len(cancels)
	deadline := time.NewTimer(c.baseline)
	defer deadline.Stop()
	for remaining > 0 {
		select {
		case <-baselines:
			remaining--
		case <-deadline.C:
			remaining = 0
		case <-ctx.Done():
			for _, cancel := range cancels {
				cancel()
			}
			return ctx.Err()
		}
	}

	// 7-8. Install the new scheduler and resume placement work.
	c.mu.Lock()
	c.cancels = cancels
	c.scheduler = sched
	c.serving = true
	c.mu.Unlock()
	return nil
}

// Stop cancels all worker streams and makes placement unavailable.
func (c *PlacementController) Stop() {
	c.mu.Lock()
	c.serving = false
	c.scheduler = nil
	c.stopStreamsLocked()
	c.mu.Unlock()
}

func (c *PlacementController) stopStreamsLocked() {
	for _, cancel := range c.cancels {
		cancel()
	}
	c.cancels = nil
}

// Version returns the active configuration version, or 0 before the first
// policy is installed.
func (c *PlacementController) Version() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// routingNeedsLeafLoad reports whether the ingress policy reads leaf_load.
// The leaf needs worker load to produce that scalar, even when placement does
// not request worker load for its own decisions.
func routingNeedsLeafLoad(routing *core.RoutingPolicyConfig) bool {
	return routing != nil && (routing.GetBoundedLoads() != nil || routing.GetRjCh() != nil || routing.GetChRlu() != nil)
}

var _ autoscaling.PlacementScheduler = (*PlacementController)(nil)
