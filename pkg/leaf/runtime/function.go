package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/autoscaling"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
)

type functionController struct {
	functionID uint64

	cfg    leaf.LeafConfig
	logger *slog.Logger

	policy   autoscaling.Policy
	actuator *autoscaling.SandboxActuator

	mu   sync.Mutex
	spec *core.FunctionSpec

	ctx    context.Context
	cancel context.CancelFunc

	demandWake chan struct{}
}

func newFunctionController(parent context.Context, spec *core.FunctionSpec, pool *dataplane.SandboxPool, rt *Runtime, policy autoscaling.Policy) *functionController {
	ctx, cancel := context.WithCancel(parent)
	cloned := cloneSpec(spec)
	fc := &functionController{
		functionID: spec.GetFunctionId(),
		spec:       cloned,
		cfg:        rt.cfg,
		logger:     rt.logger.With("function_id", spec.GetFunctionId()),
		policy:     policy,
		ctx:        ctx,
		cancel:     cancel,
		demandWake: make(chan struct{}, 1),
	}
	fc.actuator = autoscaling.NewSandboxActuator(autoscaling.ActuatorConfig{
		FunctionID: spec.GetFunctionId(),
		Spec:       cloned,
		LeafCfg:    rt.cfg,
		Logger:     fc.logger,
		Workers:    rt.workers,
		// Placement is global: every actuator shares the controller facade, which
		// publishes the one scheduler compiled for the active policy.
		Scheduler:   rt.placementCtl,
		Placement:   rt.placement,
		WorkerState: rt.WorkerStates,
		Store:       rt.store,
		Pool:        pool,
		Reporter:    rt.reporter,
	})
	pool.SetDemandWake(fc.demandWake)
	go fc.reconcileLoop()
	return fc
}

func (f *functionController) reconcileLoop() {
	t := time.NewTicker(f.cfg.Autoscaling.ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-f.demandWake:
			f.reconcile(true)
		case <-t.C:
			f.reconcile(false)
		}
	}
}

func (f *functionController) reconcile(scaleUpOnly bool) {
	signals := f.scaleSignals()
	spec := f.currentSpec()
	decision, err := f.policy.DesiredScale(f.ctx, spec, signals, time.Now())
	if err != nil {
		f.logger.Warn("scale policy failed", "error", err)
		return
	}
	stats := f.actuator.Stats()
	effective := stats.ActualInstances + stats.PendingStarts - stats.PendingStops
	if scaleUpOnly && int(decision.GetDesiredInstances()) <= effective {
		return
	}
	// Log only when a decision changes effective scale. A demand wake that does
	// not move capacity must stay silent: the wake loop can run many times per
	// burst and per-wake logging would dominate it.
	if int(decision.GetDesiredInstances()) != effective {
		f.logger.Info("scale reconcile",
			"ready_instances", signals.ReadyInstances,
			"available_concurrency", signals.AvailableConcurrency,
			"in_flight", signals.InFlight,
			"executing", signals.Executing,
			"queue_depth", signals.QueueDepth,
			"high_water", signals.HighWater,
			"desired_instances", decision.GetDesiredInstances(),
			"actual_instances", stats.ActualInstances,
			"pending_starts", stats.PendingStarts,
			"pending_stops", stats.PendingStops,
			"reason", decision.GetReason(),
			"worker_instances", stats.WorkerInstances,
			"worker_pending_starts", stats.WorkerPending,
		)
	}
	if err := f.actuator.ApplyScale(f.ctx, spec, decision); err != nil {
		f.logger.Warn("apply scale failed", "reason", decision.GetReason(), "error", err)
	}
}

func (f *functionController) scaleSignals() autoscaling.Signals {
	ready, available := f.actuator.ReadyStats()
	stats := f.actuator.Stats()
	admission := f.actuator.AdmissionStats()
	logicalScale, hasLogicalScale := f.actuator.LogicalScale()
	return autoscaling.Signals{
		ReadyInstances:       uint32(ready),
		PendingStarts:        uint32(stats.PendingStarts),
		PendingStops:         uint32(stats.PendingStops),
		LogicalScale:         logicalScale,
		HasLogicalScale:      hasLogicalScale,
		AvailableConcurrency: available,
		InFlight:             uint64(admission.Executing + admission.Queued),
		HighWater:            uint64(f.actuator.TakeAdmissionHighWater()),
		Executing:            uint64(admission.Executing),
		QueueDepth:           uint64(admission.Queued),
		OldestQueueAge:       queueAge(admission.OldestQueued),
		LastActivity:         admission.LastActivity,
	}
}

func queueAge(oldest time.Time) time.Duration {
	if oldest.IsZero() {
		return 0
	}
	return time.Since(oldest)
}

func (f *functionController) close() {
	f.cancel()
	f.actuator.StopAll(context.Background())
}

func (f *functionController) updateSpec(spec *core.FunctionSpec) {
	f.mu.Lock()
	f.spec = cloneSpec(spec)
	f.mu.Unlock()
	f.actuator.UpdateSpec(spec)
}

func (f *functionController) currentSpec() *core.FunctionSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spec
}
