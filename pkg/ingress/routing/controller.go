package routing

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/core"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
)

// ControllerConfig wires a RoutingController.
type ControllerConfig struct {
	Topology    Topology
	DialTimeout time.Duration
	Backoff     time.Duration
	Logger      *slog.Logger
	Engine      *Engine
}

// RoutingController owns the active routing policy and the leaf state streams
// that feed it. It is the only ingress object that applies routing
// configuration and routing state; there is no separate router switcher.
type RoutingController struct {
	cfg    ControllerConfig
	engine *Engine

	mu           sync.Mutex
	model        routingModel
	version      uint64
	serving      bool
	lastRevision map[uint64]uint64
	cancels      []context.CancelFunc
}

func NewRoutingController(cfg ControllerConfig) *RoutingController {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Engine == nil {
		cfg.Engine = NewEngine()
	}
	cfg.Engine.logger = cfg.Logger
	return &RoutingController{cfg: cfg, engine: cfg.Engine}
}

// Engine returns the request-side entry point this controller drives.
func (c *RoutingController) Engine() *Engine { return c.engine }

// ApplyConfig installs a routing policy and the leaf state streams it needs.
//
// Requests are unavailable from the moment the active picker is cleared until
// the new picker is installed. The old model is dropped rather than merged.
// This is acceptable because policy configuration changes only while no
// invocation load is in flight.
func (c *RoutingController) ApplyConfig(ctx context.Context, cfg *core.RoutingPolicyConfig, version uint64) error {
	policy, err := newRoutingPolicy(cfg)
	if err != nil {
		return err
	}
	needs := policy.Needs()

	// 1. Stop accepting invocation traffic.
	// 2-5. Stop old streams, compile the new policy, and drop the old model.
	c.mu.Lock()
	c.engine.clear()
	c.stopStreamsLocked()
	c.model = policy.NewModel(c.cfg.Topology)
	c.version = version
	c.serving = false
	c.lastRevision = make(map[uint64]uint64)
	c.mu.Unlock()

	// 6. Start new streams with the policy's exact projection.
	projection := &leafpkg.RoutingStateProjection{
		LeafLoad:          needs.Has(NeedLeafLoad),
		AggregateInFlight: needs.Has(NeedAggregateInFlight),
		FunctionCapacity:  needs.Has(NeedFunctionCapacity),
	}
	ids := c.cfg.Topology.IDs()
	baselines := make(chan uint64, len(ids))
	cancels := make([]context.CancelFunc, 0, len(ids))
	for _, id := range ids {
		addr := c.cfg.Topology.ControlAddress(id)
		if addr == "" {
			continue
		}
		streamCtx, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		go c.runLeafStream(streamCtx, id, addr, version, projection, baselines)
	}
	c.mu.Lock()
	c.cancels = cancels
	c.mu.Unlock()

	// 7. Wait for a baseline from every leaf we tried to reach. A leaf that
	// cannot be dialed is skipped and retried in the background. There is no
	// preparation timeout beyond the dial timeout: reloads happen with no
	// invocation load in flight, so waiting for reachable leaves is acceptable
	// for a research platform.
	remaining := len(cancels)
	for remaining > 0 {
		select {
		case <-baselines:
			remaining--
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 8-9. Build and publish the new picker atomically.
	c.mu.Lock()
	c.serving = true
	picker := c.model.Picker()
	c.engine.install(picker, version)
	c.mu.Unlock()
	return nil
}

// Stop cancels all streams and makes routing unavailable.
func (c *RoutingController) Stop() {
	c.mu.Lock()
	c.engine.clear()
	c.serving = false
	c.stopStreamsLocked()
	c.model = nil
	c.mu.Unlock()
}

func (c *RoutingController) stopStreamsLocked() {
	for _, cancel := range c.cancels {
		cancel()
	}
	c.cancels = nil
}

// runLeafStream keeps one leaf's routing-state stream alive, reconnecting with
// backoff. It signals the baseline channel once, whether the first frame
// arrives or the leaf turns out to be unreachable.
func (c *RoutingController) runLeafStream(ctx context.Context, leafID uint64, addr string, version uint64, projection *leafpkg.RoutingStateProjection, baselines chan<- uint64) {
	signaled := false
	signal := func() {
		if signaled {
			return
		}
		signaled = true
		select {
		case baselines <- leafID:
		default:
		}
	}

	for {
		if ctx.Err() != nil {
			signal()
			return
		}
		err := c.streamSession(ctx, leafID, addr, version, projection, signal)
		// Signal after the first session ends as well: if the leaf could not be
		// dialed there is no first frame, but ApplyConfig must not wait for a
		// leaf that is simply unreachable.
		signal()
		if ctx.Err() != nil {
			return
		}
		// A dropped stream means the leaf's state is no longer trustworthy.
		c.markUnhealthy(leafID, version)
		if err != nil {
			c.cfg.Logger.Warn("leaf routing-state stream ended", "leaf_id", leafID, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.cfg.Backoff):
		}
	}
}

func (c *RoutingController) streamSession(ctx context.Context, leafID uint64, addr string, version uint64, projection *leafpkg.RoutingStateProjection, signal func()) error {
	//TODO: modernize , not dialcontext and not grpc.WithBlock()
	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	conn, err := grpc.DialContext(dialCtx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()

	client := leafpkg.NewLeafControlServiceClient(conn)
	stream, err := client.WatchRoutingState(ctx, &leafpkg.WatchRoutingStateRequest{
		ConfigVersion: version,
		Projection:    projection,
	})
	if err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if frame.GetLeafId() != leafID {
			continue
		}
		if c.onFrame(leafStateFromProto(frame)) && frame.GetFullSnapshot() {
			signal()
		}
	}
}

// onFrame accepts a frame only for the active configuration and only when its
// revision is newer than the last one applied for that leaf.
func (c *RoutingController) onFrame(s LeafState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.model == nil || s.ConfigVersion != c.version {
		return false
	}
	if last, ok := c.lastRevision[s.LeafID]; ok && s.Revision <= last {
		return false
	}
	c.cfg.Logger.Debug("routing frame",
		"leaf_id", s.LeafID,
		"revision", s.Revision,
		"full_snapshot", s.FullSnapshot,
		"healthy_workers", s.HealthyWorkers,
		"has_leaf_load", s.HasLeafLoad,
		"leaf_load", s.LeafLoad,
		"serving", c.serving,
	)
	c.lastRevision[s.LeafID] = s.Revision
	var result UpdateResult
	if s.FullSnapshot {
		result = c.model.ReplaceLeaf(s)
	} else {
		result = c.model.Apply(s)
	}
	if c.serving && result == PublishPicker {
		c.engine.install(c.model.Picker(), c.version)
	}
	return true
}

// markUnhealthy removes a leaf from routing after its stream drops.
func (c *RoutingController) markUnhealthy(leafID, version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.model == nil || version != c.version {
		return
	}
	if c.model.LeafDisconnected(leafID) == PublishPicker && c.serving {
		c.engine.install(c.model.Picker(), c.version)
	}
}

// leafStateFromProto translates the wire frame into the routing-local state so
// policy models do not depend on the leaf proto.
func leafStateFromProto(f *leafpkg.RoutingStateFrame) LeafState {
	s := LeafState{
		ConfigVersion:  f.GetConfigVersion(),
		LeafID:         f.GetLeafId(),
		Revision:       f.GetRevision(),
		FullSnapshot:   f.GetFullSnapshot(),
		HealthyWorkers: f.GetHealthyWorkers(),
		Capacities:     f.GetCapacities(),
	}
	if f.LeafLoad != nil {
		s.LeafLoad = f.GetLeafLoad()
		s.HasLeafLoad = true
	}
	if f.AggregateInFlight != nil {
		s.AggregateInFlight = f.GetAggregateInFlight()
		s.HasAggregateInFlight = true
	}
	return s
}
