package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/controlplaneclient"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/autoscaling"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
	"hyperfaas-ideal-arch/pkg/leaf/grpcproxy"
	"hyperfaas-ideal-arch/pkg/leaf/scheduler"
	leafstate "hyperfaas-ideal-arch/pkg/leaf/state"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
)

// Runtime is the concrete leaf implementation.
type Runtime struct {
	cfg    leaf.LeafConfig
	logger *slog.Logger

	mu          sync.RWMutex
	functions   atomic.Pointer[functionTable]
	controllers map[uint64]*functionController

	store *dataplane.Store

	workers []*leafworker.Client
	policy  autoscaling.Policy

	// workerStates is the leaf-wide worker telemetry cache. It is fed by the
	// placement controller's projection-gated streams, so it only contains the
	// gated signals the active policy requested plus always-on lifecycle state.
	// HealthyWorkers and LeafLoad read it for the routing signal.
	workerStateMu sync.RWMutex
	workerStates  []*core.WorkerState
	placement     *PlacementState
	// placementCtl owns the active placement policy and the projection-gated
	// worker state streams. It is also the stable scheduler facade shared by
	// every function actuator.
	placementCtl *PlacementController

	reporter *leafstate.Reporter

	// configSync streams the dynamic platform-config document. Accepted
	// documents are coalesced into pendingConfig and applied by
	// configReloadLoop so the subscriber goroutine never blocks on a reload.
	configSync    *controlplaneclient.ConfigSubscriber
	pendingConfig atomic.Pointer[core.PlatformConfig]
	configWake    chan struct{}
	// heartbeatNanos is the state_refresh_interval from the active document, in
	// nanoseconds. It drives the routing heartbeat and falls back to the static
	// YAML value until the first document arrives.
	heartbeatNanos atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc
}

// functionRuntime is the immutable request-path view of a deployed function.
// Lifecycle and autoscaling keep their full FunctionSpec separately.
type functionTable struct {
	byID map[uint64]*dataplane.Function
}

// NewRuntime wires the leaf runtime from configuration.
func NewRuntime(ctx context.Context, cfg leaf.LeafConfig, subscriber leaf.FunctionSubscriber, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		return nil, fmt.Errorf("leaf runtime: logger is required")
	}
	if subscriber == nil {
		subscriber = controlplaneclient.NewFunctionSubscriber(
			cfg.ControlPlane.Address,
			cfg.ControlPlane.DialTimeout,
			cfg.Dataplane.StatusBackoff,
			logger,
		)
	}

	rtCtx, cancel := context.WithCancel(ctx)
	rt := &Runtime{
		cfg:         cfg,
		logger:      logger.With("component", "leaf-runtime"),
		controllers: make(map[uint64]*functionController),
		store:       dataplane.NewStore(),
		ctx:         rtCtx,
		cancel:      cancel,
	}
	rt.functions.Store(&functionTable{byID: make(map[uint64]*dataplane.Function)})
	rt.reporter = leafstate.NewReporter(cfg.LeafID, rt)
	workers := make([]*leafworker.Client, 0, len(cfg.Workers))
	for i, w := range cfg.Workers {
		client, err := leafworker.NewClient(rtCtx, i, w.Address, cfg.ControlPlane.DialTimeout, cfg.Dataplane.StartTimeout, cfg.Dataplane.StopTimeout, logger)
		if err != nil {
			cancel()
			return nil, err
		}
		workers = append(workers, client)
	}
	rt.workers = workers
	rt.workerStates = make([]*core.WorkerState, len(workers))
	startTokens := placementStartTokens(cfg.Dataplane)
	rt.placement = NewPlacementState(len(workers), startTokens,
		placementStartLimit(cfg.Dataplane, len(workers), startTokens), cfg.Dataplane.MaxInstancesPerWorker)
	// The placement controller owns the gated worker-state streams. Lifecycle
	// sandbox-removal events stay on the always-on event watch below because
	// they are not part of any placement projection.
	placementWorkers := make([]placementWorker, len(workers))
	for i, w := range workers {
		placementWorkers[i] = w
	}
	rt.placementCtl = NewPlacementController(
		placementWorkers,
		rt.placement,
		cfg.Dataplane.StatusBackoff,
		cfg.Dataplane.MaxInstancesPerWorker,
		cfg.Dataplane.DialTimeout,
		rt.logger,
		rt.setWorkerState,
		rt.resetWorkerTelemetry,
	)
	rt.watchWorkerEvents()

	rt.policy = autoscaling.NewPolicy(cfg)

	events, errs := subscriber.SubscribeFunctions(rtCtx)
	go rt.watchFunctions(events, errs)
	go rt.routingHeartbeatLoop()

	// The leaf receives the same versioned platform config as ingress. Accepted
	// documents are coalesced and applied on configReloadLoop: placement policy
	// reload and state_refresh_interval are both derived from the document.
	if cfg.ControlPlane.Address != "" {
		rt.configWake = make(chan struct{}, 1)
		rt.configSync = controlplaneclient.NewConfigSubscriber(
			cfg.ControlPlane.Address,
			cfg.ControlPlane.DialTimeout,
			cfg.Dataplane.StatusBackoff,
			rt.logger,
			rt.onPlatformConfig,
		)
		go rt.configSync.Run(rtCtx)
		go rt.configReloadLoop()
	}

	return rt, nil
}

// onPlatformConfig runs on the config subscriber goroutine. It only stores the
// document and wakes the reload loop so a reload never blocks the watch.
func (r *Runtime) onPlatformConfig(cfg *core.PlatformConfig) {
	if cfg == nil {
		return
	}
	r.pendingConfig.Store(cfg)
	select {
	case r.configWake <- struct{}{}:
	default:
	}
}

// configReloadLoop applies the latest accepted platform config. Coalescing to
// the newest document matches the watch's latest-wins delivery: intermediate
// versions are skipped because configuration is desired state.
func (r *Runtime) configReloadLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.configWake:
			r.applyPlatformConfig(r.pendingConfig.Load())
		}
	}
}

// applyPlatformConfig installs the placement policy and updates the state
// refresh interval from one document.
func (r *Runtime) applyPlatformConfig(cfg *core.PlatformConfig) {
	if cfg == nil {
		return
	}
	if interval := cfg.GetStateRefreshInterval(); interval != nil {
		if d := interval.AsDuration(); d > 0 {
			r.heartbeatNanos.Store(int64(d))
		}
	}
	placement := cfg.GetPlacement()
	if placement == nil || placement.GetPolicy() == nil {
		return
	}
	// Reloads only happen with no invocation load in flight. Placement is
	// rejected while the controller is not serving, and there is no fallback to
	// the previous policy.
	if err := r.placementCtl.ApplyConfig(r.ctx, placement, cfg.GetRouting(), cfg.GetVersion()); err != nil {
		r.logger.Warn("failed to apply placement policy",
			"config_version", cfg.GetVersion(),
			"error", err,
		)
		return
	}
	r.logger.Info("applied placement policy",
		"policy", scheduler.PlacementPolicyLabel(placement),
		"config_version", cfg.GetVersion(),
	)
}

// resetWorkerTelemetry drops the previous policy's telemetry so the new
// scheduler never reads signals retained for the old policy.
func (r *Runtime) resetWorkerTelemetry() {
	r.workerStateMu.Lock()
	for i := range r.workerStates {
		r.workerStates[i] = nil
	}
	r.workerStateMu.Unlock()
	r.placement.ResetTelemetry()
}

func placementStartTokens(cfg leaf.DataplaneConfig) uint32 {
	if cfg.DirigentStrictAdmission {
		// Dirigent's doUpscaling dispatches every missing replica without a
		// per-worker cold-start token gate. Keep placement otherwise unchanged.
		return 0
	}
	if cfg.StartTokensPerWorker == 0 {
		return 8
	}
	return cfg.StartTokensPerWorker
}

func placementStartLimit(cfg leaf.DataplaneConfig, workerCount int, perWorker uint32) uint64 {
	if cfg.DirigentStrictAdmission {
		return 0
	}
	if cfg.MaxStartsPerReconcile > 0 {
		return uint64(cfg.MaxStartsPerReconcile)
	}
	return uint64(workerCount) * uint64(perWorker)
}

// heartbeatInterval returns the active state refresh interval. The dynamic
// config document wins; the static YAML value is only a bootstrap fallback until
// the first document arrives.
func (r *Runtime) heartbeatInterval() time.Duration {
	if n := r.heartbeatNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return r.cfg.Dataplane.RoutingStateHeartbeatInterval
}

func (r *Runtime) routingHeartbeatLoop() {
	// A timer (not a ticker) so a config change to state_refresh_interval takes
	// effect on the next beat without restarting the loop.
	for {
		t := time.NewTimer(r.heartbeatInterval())
		select {
		case <-r.ctx.Done():
			t.Stop()
			return
		case <-t.C:
			r.reporter.PublishHeartbeat()
		}
	}
}

func (r *Runtime) GetFunction(functionID uint64) *dataplane.Function {
	return r.functions.Load().byID[functionID]
}

func requestFunctionFromSpec(spec *core.FunctionSpec, pool *dataplane.SandboxPool) *dataplane.Function {
	function := &dataplane.Function{
		ID:       spec.GetFunctionId(),
		Protocol: spec.GetRuntime().GetProtocol(),
		Pool:     pool,
	}
	if scale := spec.GetScale(); scale != nil {
		if timeout := scale.GetRequestTimeout(); timeout != nil {
			function.RequestTimeout = timeout.AsDuration()
		}
	}
	return function
}

// publishFunctionLocked replaces the immutable request registry. r.mu must be held.
func (r *Runtime) publishFunctionLocked(function *dataplane.Function) {
	current := r.functions.Load()
	next := make(map[uint64]*dataplane.Function, len(current.byID)+1)
	for id, existing := range current.byID {
		next[id] = existing
	}
	next[function.ID] = function
	r.functions.Store(&functionTable{byID: next})
}

// deleteFunctionLocked removes one request-path record. r.mu must be held.
func (r *Runtime) deleteFunctionLocked(functionID uint64) {
	current := r.functions.Load()
	next := make(map[uint64]*dataplane.Function, len(current.byID))
	for id, existing := range current.byID {
		if id != functionID {
			next[id] = existing
		}
	}
	r.functions.Store(&functionTable{byID: next})
}

func (r *Runtime) watchFunctions(events <-chan *core.FunctionEvent, errs <-chan error) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case err := <-errs:
			if err != nil {
				r.logger.Warn("function watch error", "error", err)
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			r.handleFunctionEvent(ev)
		}
	}
}

func (r *Runtime) handleFunctionEvent(ev *core.FunctionEvent) {
	switch ev.GetType() {
	case core.FunctionEventType_FUNCTION_EVENT_TYPE_CREATED, core.FunctionEventType_FUNCTION_EVENT_TYPE_UPDATED:
		_ = r.ApplyFunction(r.ctx, ev.GetFunction())
	case core.FunctionEventType_FUNCTION_EVENT_TYPE_DELETED:
		_ = r.DeleteFunction(r.ctx, ev.GetFunction().GetFunctionId())
	}
}

func (r *Runtime) ApplyFunction(ctx context.Context, function *core.FunctionSpec) error {
	_ = ctx
	if err := validateFunction(function); err != nil {
		return err
	}
	var minInstances, maxInstances, maxConcurrency, queueDepth uint64
	if scale := function.GetScale(); scale != nil {
		minInstances = scale.GetMinInstances()
		maxInstances = scale.GetMaxInstances()
		maxConcurrency = scale.GetMaxConcurrency()
		queueDepth = scale.GetMaxQueueDepth()
	}
	r.logger.Info("function applied",
		"function_id", function.GetFunctionId(),
		"protocol", function.GetRuntime().GetProtocol(),
		"min_instances", minInstances,
		"max_instances", maxInstances,
		"max_concurrency", maxConcurrency,
		"max_queue_depth", queueDepth,
	)
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.GetFunction(function.GetFunctionId())
	pool := (*dataplane.SandboxPool)(nil)
	if current != nil {
		pool = current.Pool
	} else {
		pool = dataplane.NewSandboxPool(int(maxConcurrency), int(queueDepth))
	}
	pool.Configure(int(maxConcurrency), int(queueDepth))
	if ctrl, ok := r.controllers[function.GetFunctionId()]; ok {
		ctrl.updateSpec(function)
		r.publishFunctionLocked(requestFunctionFromSpec(function, pool))
		return nil
	}
	ctrl := newFunctionController(r.ctx, function, pool, r, r.policy)
	r.controllers[function.GetFunctionId()] = ctrl
	r.publishFunctionLocked(requestFunctionFromSpec(function, pool))
	ctrl.actuator.PublishCapacity()
	return nil
}

func (r *Runtime) DeleteFunction(ctx context.Context, functionID uint64) error {
	_ = ctx
	r.mu.Lock()
	function := r.functions.Load().byID[functionID]
	r.deleteFunctionLocked(functionID)
	ctrl, ok := r.controllers[functionID]
	if ok {
		delete(r.controllers, functionID)
	}
	r.mu.Unlock()
	if ok {
		ctrl.close()
	}
	if function != nil {
		function.Pool.Close()
	}
	r.store.RemoveFunction(functionID)
	r.reporter.PublishDeleted(functionID)
	r.logger.Info("function deleted", "function_id", functionID)
	return nil
}

func (r *Runtime) EnsureCapacity(ctx context.Context, demand *core.ScaleDemand) error {
	if demand == nil || demand.GetFunctionId() == 0 {
		return status.Error(codes.InvalidArgument, "scale demand is required")
	}
	r.mu.RLock()
	ctrl, ok := r.controllers[demand.GetFunctionId()]
	r.mu.RUnlock()
	if !ok {
		return status.Errorf(codes.NotFound, "function %d not found", demand.GetFunctionId())
	}
	actual := int(ctrl.actuator.ActualInstances())
	delta := demand.GetDelta()
	if delta <= 0 && demand.GetDesiredInstances() <= uint64(actual) {
		return nil
	}
	count := int(demand.GetDesiredInstances()) - actual
	if demand.GetDelta() > 0 {
		count = int(demand.GetDelta())
	}
	if count <= 0 {
		count = 1
	}
	for range count {
		if err := ctrl.actuator.StartOne(ctx, demand.GetReason()); err != nil {
			return err
		}
	}
	return nil
}

// CurrentState returns the diagnostic full leaf snapshot. It is only for DST
// tooling and tests; it computes every field and is not on the routing path.
func (r *Runtime) CurrentState(ctx context.Context) (*core.LeafState, error) {
	return r.reporter.CurrentState(ctx)
}

// WatchRoutingState streams projection-gated routing frames to one subscriber.
func (r *Runtime) WatchRoutingState(ctx context.Context, configVersion uint64, projection leafstate.RoutingProjection) (<-chan *leafstate.RoutingFrame, <-chan error) {
	return r.reporter.WatchRoutingState(ctx, configVersion, projection)
}

func (r *Runtime) CapacitySnapshot() map[uint64]*core.FunctionCapacity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	functions := r.functions.Load()
	out := make(map[uint64]*core.FunctionCapacity, len(functions.byID))
	for functionID, function := range functions.byID {
		snapshot := function.Pool.Snapshot()
		ready := uint32(snapshot.ReadyInstances)
		st := core.CapacityStatus_CAPACITY_STATUS_COLD
		if ready > 0 {
			st = core.CapacityStatus_CAPACITY_STATUS_AVAILABLE
		}
		out[functionID] = &core.FunctionCapacity{
			FunctionId:           functionID,
			ReadyInstances:       ready,
			AvailableConcurrency: snapshot.AvailableConcurrency,
			InFlight:             uint64(snapshot.Executing),
			Status:               st,
			ObservedAt:           timestamppb.Now(),
		}
	}
	return out
}

// WorkerHealthy reports whether any worker answers the unary diagnostic
// CurrentState read. It is a diagnostic probe, not the routing health signal;
// routing uses HealthyWorkers, which counts workers that can accept sandboxes.
func (r *Runtime) WorkerHealthy() bool {
	for _, w := range r.workers {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := w.CurrentState(ctx)
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}

func (r *Runtime) watchWorkerEvents() {
	for _, worker := range r.workers {
		w := worker
		// Lifecycle sandbox-removal events are not projection gated: the leaf
		// must always evict instances that leave a worker outside the normal
		// StopSandbox path. The periodic gated WorkerState stream is owned by
		// the placement controller instead.
		w.StartEventWatch(r.ctx, r.cfg.Dataplane.StatusBackoff, func(functionID, instanceID, workerRevision uint64) {
			r.handleSandboxRemoved(w.Index(), functionID, instanceID, workerRevision)
		})
	}
}

// handleSandboxRemoved routes a worker removal event to the owning function's
// actuator. This is the low-latency eviction path; setWorkerState's relist is
// the anti-entropy backstop.
func (r *Runtime) handleSandboxRemoved(workerIdx int, functionID, instanceID, workerRevision uint64) {
	r.mu.RLock()
	controller := r.controllers[functionID]
	r.mu.RUnlock()
	if controller == nil || controller.actuator == nil {
		return
	}
	controller.actuator.HandleSandboxRemoved(workerIdx, instanceID, workerRevision)
}

func (r *Runtime) setWorkerState(index int, state *core.WorkerState) {
	if index < 0 || index >= len(r.workerStates) || state == nil {
		return
	}
	// Clone before publishing into the runtime cache so later mutations by the
	// gRPC layer or caller cannot race with scheduler reads.
	cloned := proto.Clone(state).(*core.WorkerState)
	cloned.WorkerId = uint64(index + 1)
	r.workerStateMu.Lock()
	r.workerStates[index] = cloned
	r.workerStateMu.Unlock()
	r.placement.UpdateWorker(index, cloned)

	// WorkerState contains the worker's complete sandbox list. Reconcile it
	// against every per-function actuator so exits outside the normal leaf stop
	// path cannot leave a dead address in the routing store. Build the presence
	// set once and share it so the cost stays O(instances), not O(functions x
	// instances).
	presence := autoscaling.NewWorkerPresence(cloned)
	r.mu.RLock()
	actuators := make([]*autoscaling.SandboxActuator, 0, len(r.controllers))
	for _, controller := range r.controllers {
		if controller != nil && controller.actuator != nil {
			actuators = append(actuators, controller.actuator)
		}
	}
	r.mu.RUnlock()
	for _, actuator := range actuators {
		actuator.ReconcileWorkerState(index, presence)
	}
}

func (r *Runtime) WorkerStates() []*core.WorkerState {
	r.workerStateMu.RLock()
	defer r.workerStateMu.RUnlock()
	out := make([]*core.WorkerState, len(r.workerStates))
	for i, state := range r.workerStates {
		if state != nil {
			// Return clones because actuators overwrite Instances with leaf-local
			// actual+pending counts before handing the snapshot to schedulers.
			out[i] = proto.Clone(state).(*core.WorkerState)
		}
	}
	return out
}

func (r *Runtime) LeafLoad() float64 {
	r.workerStateMu.RLock()
	defer r.workerStateMu.RUnlock()
	var max float64
	for _, state := range r.workerStates {
		if state == nil {
			continue
		}
		if v := state.GetLoadAverageNorm(); v > max {
			max = v
		}
	}
	return max
}

// HealthyWorkers returns the number of workers that can currently accept new
// sandboxes. Routing must not treat stream liveness as health: a leaf process
// can stay connected while every worker is unusable.
func (r *Runtime) HealthyWorkers() uint32 {
	r.workerStateMu.RLock()
	defer r.workerStateMu.RUnlock()
	var healthy uint32
	for _, state := range r.workerStates {
		if state != nil && state.GetHealthy() {
			healthy++
		}
	}
	return healthy
}

// AggregateInFlight returns the total in-flight invocation count across all
// functions without building any protobuf capacity messages. The least-loaded
// routing policy consumes this single scalar, so ingress never needs the
// per-function capacity entries just to compute it.
func (r *Runtime) AggregateInFlight() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	functions := r.functions.Load()
	var total uint64
	for _, function := range functions.byID {
		total += uint64(function.Pool.Snapshot().Executing)
	}
	return total
}

func (r *Runtime) Close() {
	r.cancel()
	for _, w := range r.workers {
		_ = w.Close()
	}
}

func validateFunction(function *core.FunctionSpec) error {
	if function == nil || function.GetFunctionId() == 0 {
		return status.Error(codes.InvalidArgument, "function is required")
	}
	if function.GetRuntime() == nil || function.GetRuntime().GetImage() == "" {
		return status.Error(codes.InvalidArgument, "function.runtime.image is required")
	}
	protocol := function.GetRuntime().GetProtocol()
	if protocol != "http" && protocol != "grpc" {
		return status.Errorf(codes.InvalidArgument, "unsupported protocol %q", protocol)
	}
	return nil
}

// LeaseForProxy implements grpcproxy.LeaseInvoker.
func (r *Runtime) LeaseForProxy(ctx context.Context, functionID uint64) (*dataplane.Lease, *grpc.ClientConn, error) {
	function := r.GetFunction(functionID)
	if function == nil {
		return nil, nil, status.Error(codes.NotFound, "function not found")
	}
	if function.Protocol != "grpc" {
		return nil, nil, status.Error(codes.Unimplemented, "function protocol is not gRPC")
	}
	lease, err := function.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpcproxy.DialBackend(ctx, lease.Sandbox().Address)
	if err != nil {
		lease.Release()
		return nil, nil, err
	}
	return &lease, conn, nil
}

func cloneSpec(in *core.FunctionSpec) *core.FunctionSpec {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*core.FunctionSpec)
}
