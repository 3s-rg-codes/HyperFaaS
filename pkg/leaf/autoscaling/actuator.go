package autoscaling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
)

// defaultStartBurstPerWorker is used when start_tokens_per_worker and
// max_starts_per_reconcile are both unset, to avoid unbounded scale-up stampedes.
// Dirigent-copy admission deliberately bypasses this guard: Dirigent dispatches
// every missing replica from an upscaling decision concurrently.
const defaultStartBurstPerWorker = 8

type PlacementScheduler interface {
	PickWorker(ctx context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error)
}

type PlacementChoice struct {
	WorkerID uint64
	Reason   string
}

type PlacementReservation struct {
	WorkerIndex int
	Image       string
	Slot        int
	Generation  uint64
	Valid       bool
}

// ScheduleTiming breaks down PlacementState.Schedule wall time.
type ScheduleTiming struct {
	LockWaitMs int64 // time blocked acquiring PlacementState.mu
	HoldMs     int64 // time holding PlacementState.mu
	PickMs     int64 // time inside PickWorker (subset of HoldMs)
}

type PlacementCoordinator interface {
	Schedule(ctx context.Context, scheduler PlacementScheduler, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (PlacementChoice, PlacementReservation, []*core.WorkerState, ScheduleTiming, error)
	CommitReservation(PlacementReservation)
	CancelReservation(PlacementReservation)
	RecordStop(workerID uint64)
}

type placementStartCapacityNotifier interface {
	StartCapacityChanged() <-chan struct{}
}

type statePublisher interface {
	Publish(cap *core.FunctionCapacity)
}

type trackedInstance struct {
	id                  uint64
	address             string
	stopping            bool
	workerStateRevision uint64
}

// removedInstance is an instance evicted from the leaf's tracked set, either by
// a periodic relist or by a worker removal event.
type removedInstance struct {
	id             uint64
	address        string
	createRevision uint64
	stopping       bool
}

// WorkerPresence is the set of sandbox instance IDs a worker reported in one
// revisioned WorkerState snapshot. It is built once per snapshot and shared
// across every function actuator so relisting is O(instances) rather than
// O(functions x instances). Instance IDs are globally unique (leaf-assigned),
// so a single set serves all functions.
type WorkerPresence struct {
	Revision    uint64
	InstanceIDs map[uint64]struct{}
}

// NewWorkerPresence builds the shared presence set from one WorkerState snapshot.
func NewWorkerPresence(state *core.WorkerState) WorkerPresence {
	if state == nil {
		return WorkerPresence{}
	}
	instances := state.GetSandboxStates()
	ids := make(map[uint64]struct{}, len(instances))
	for _, instance := range instances {
		ids[instance.GetInstanceId()] = struct{}{}
	}
	return WorkerPresence{Revision: state.GetSandboxRevision(), InstanceIDs: ids}
}

type workerInstanceState struct {
	instances     []trackedInstance
	pendingStarts int
}

type ActuatorStats struct {
	ActualInstances int
	PendingStarts   int
	PendingStops    int
	WorkerInstances []uint64
	WorkerPending   []uint64
}

// SandboxActuator starts and stops sandboxes through worker RPCs.
type SandboxActuator struct {
	functionID uint64
	spec       *core.FunctionSpec

	cfg    leaf.LeafConfig
	logger *slog.Logger

	workers     []*leafworker.Client
	scheduler   PlacementScheduler
	placement   PlacementCoordinator
	store       *dataplane.Store
	pool        *dataplane.SandboxPool
	reporter    statePublisher
	workerState func() []*core.WorkerState

	mu              sync.Mutex
	workerInstances []workerInstanceState
	totalInstances  int
	pendingStarts   int
	pendingStops    int
	targetInstances int
	targetReason    string
	targetRevision  uint64
	scaleUpWake     chan struct{}
	scaleUpActive   bool

	// Strict admission mirrors Dirigent's unbuffered desired-state handoff:
	// a later decision waits for the active batch instead of replacing it.
	strictBatchActive     bool
	strictBatchDone       chan struct{}
	strictOptimisticScale int
	strictOptimisticKnown bool
}

type ActuatorConfig struct {
	FunctionID  uint64
	Spec        *core.FunctionSpec
	LeafCfg     leaf.LeafConfig
	Logger      *slog.Logger
	Workers     []*leafworker.Client
	Scheduler   PlacementScheduler
	Placement   PlacementCoordinator
	Store       *dataplane.Store
	Pool        *dataplane.SandboxPool
	Reporter    statePublisher
	WorkerState func() []*core.WorkerState
}

func NewSandboxActuator(cfg ActuatorConfig) *SandboxActuator {
	a := &SandboxActuator{
		functionID:      cfg.FunctionID,
		spec:            cfg.Spec,
		cfg:             cfg.LeafCfg,
		logger:          cfg.Logger,
		workers:         cfg.Workers,
		scheduler:       cfg.Scheduler,
		placement:       cfg.Placement,
		store:           cfg.Store,
		pool:            cfg.Pool,
		reporter:        cfg.Reporter,
		workerState:     cfg.WorkerState,
		workerInstances: make([]workerInstanceState, len(cfg.Workers)),
		scaleUpWake:     make(chan struct{}, 1),
	}
	for i := range a.workerInstances {
		capacity := 64
		if max := int(cfg.LeafCfg.Dataplane.MaxInstancesPerWorker); max > 0 {
			capacity = max
		}
		a.workerInstances[i].instances = make([]trackedInstance, 0, capacity)
	}
	return a
}

func (a *SandboxActuator) ActualInstances() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return uint64(a.totalInstances)
}

// LogicalScale reports Dirigent's optimistic actual scale after its first
// strict decision. It intentionally does not alter externally reported ready
// capacity while asynchronous teardown is still in progress.
func (a *SandboxActuator) LogicalScale() (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.Dataplane.DirigentStrictAdmission || !a.strictOptimisticKnown || a.strictOptimisticScale < 0 {
		return 0, false
	}
	return uint64(a.strictOptimisticScale), true
}

func (a *SandboxActuator) Stats() ActuatorStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	stats := ActuatorStats{
		ActualInstances: a.totalInstances,
		PendingStarts:   a.pendingStarts,
		PendingStops:    a.pendingStops,
		WorkerInstances: make([]uint64, len(a.workerInstances)),
		WorkerPending:   make([]uint64, len(a.workerInstances)),
	}
	for i := range a.workerInstances {
		stats.WorkerInstances[i] = uint64(len(a.workerInstances[i].instances))
		stats.WorkerPending[i] = uint64(a.workerInstances[i].pendingStarts)
	}
	return stats
}

// UpdateSpec replaces the function spec. The scheduler is not updated here:
// placement is global and the actuator holds the placement controller facade,
// which publishes the active policy's scheduler.
func (a *SandboxActuator) UpdateSpec(spec *core.FunctionSpec) {
	a.mu.Lock()
	a.spec = spec
	a.mu.Unlock()
}

func (a *SandboxActuator) ApplyScale(ctx context.Context, function *core.FunctionSpec, decision *core.ScaleDecision) error {
	_ = function
	desired := int(decision.GetDesiredInstances())
	reason := decision.GetReason()
	if a.cfg.Dataplane.DirigentStrictAdmission {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			a.mu.Lock()
			if !a.strictBatchActive {
				previous := a.strictOptimisticScale
				a.strictOptimisticScale = desired
				a.strictOptimisticKnown = true
				done := make(chan struct{})
				a.strictBatchDone = done
				a.strictBatchActive = true
				a.mu.Unlock()
				go a.runStrictBatchAndRelease(ctx, previous, desired, reason, done)
				return nil
			}
			done := a.strictBatchDone
			a.mu.Unlock()

			// This is the equivalent of sending through Dirigent's unbuffered
			// DesiredStateChannel while ScalingControllerLoop is busy.
			waitStart := time.Now()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				a.logger.Debug("strict_batch_wait",
					"function_id", a.functionID,
					"wait_ms", time.Since(waitStart).Milliseconds(),
					"desired_instances", desired,
					"reason", reason,
				)
			}
		}
	}

	a.mu.Lock()
	if a.targetInstances != desired {
		a.targetRevision++
	}
	a.targetInstances = desired
	a.targetReason = reason
	actual := a.totalInstances
	pending := a.pendingStarts
	pendingStops := a.pendingStops
	future := actual + pending - pendingStops
	startPump := desired != future && !a.scaleUpActive
	if startPump {
		a.scaleUpActive = true
	}
	a.mu.Unlock()
	a.wakeScaleUp()

	effective := future

	if desired == effective {
		return nil
	}
	if desired > effective {
		gap := desired - effective
		fields := []any{
			"desired_instances", desired,
			"actual_instances", actual,
			"pending_starts", pending,
			"pending_stops", pendingStops,
			"start_gap", gap,
			"max_concurrent_starts", a.maxStartsPerReconcile(),
			"reason", reason,
		}
		a.logger.Info("scale up requested", fields...)
		if startPump {
			go a.scalePump(ctx)
		}
		return nil
	}
	if desired < effective {
		a.logger.Info("scale down requested",
			"desired_instances", desired,
			"actual_instances", actual,
			"pending_starts", pending,
			"pending_stops", pendingStops,
			"stop_count", effective-desired,
			"reason", decision.GetReason(),
		)
		if startPump {
			go a.scalePump(ctx)
		}
		return nil
	}
	return nil
}

func (a *SandboxActuator) runStrictBatchAndRelease(ctx context.Context, previous, desired int, reason string, done chan struct{}) {
	dispatched, succeeded, failed := a.runStrictBatch(ctx, previous, desired, reason)
	a.logger.Info("Dirigent strict batch completed",
		"desired_instances", desired,
		"previous_optimistic_scale", previous,
		"dispatched_count", dispatched,
		"successful_count", succeeded,
		"failed_count", failed,
		"reason", reason,
	)

	a.mu.Lock()
	a.strictBatchActive = false
	close(done)
	a.mu.Unlock()
}

func (a *SandboxActuator) runStrictBatch(ctx context.Context, previous, desired int, reason string) (dispatched, succeeded, failed int) {
	if desired <= previous {
		if desired < previous {
			a.logger.Info("Dirigent strict batch dispatch",
				"desired_instances", desired,
				"previous_optimistic_scale", previous,
				"dispatched_count", 0,
				"reason", reason,
			)
			// Reserve and exclude victims before releasing the strict handoff;
			// draining and stopping them must not block the scaling loop.
			stopList := a.instancesForStop(previous - desired)
			go func() { _ = a.stopInstances(ctx, stopList) }()
		}
		return 0, 0, 0
	}

	toCreate := desired - previous
	a.logger.Info("Dirigent strict batch dispatch",
		"desired_instances", desired,
		"previous_optimistic_scale", previous,
		"batch_count", toCreate,
		"reason", reason,
	)
	dispatched = toCreate
	a.logger.Info("Dirigent strict batch dispatched",
		"desired_instances", desired,
		"previous_optimistic_scale", previous,
		"dispatched_count", dispatched,
		"reason", reason,
	)
	var wg sync.WaitGroup
	var resultsMu sync.Mutex
	wg.Add(toCreate)
	for range toCreate {
		go func() {
			defer wg.Done()
			// Dirigent performs placement inside each creation goroutine. Do not
			// reserve an entire batch before any CreateSandbox RPC can begin.
			job, err := a.planStart(ctx, reason, false)
			if err != nil || job == nil {
				resultsMu.Lock()
				failed++
				resultsMu.Unlock()
				a.strictBatchFailed()
				if err != nil && ctx.Err() == nil {
					a.logger.Warn("Dirigent strict batch placement failed", "error", err)
				}
				return
			}
			if err := a.runStart(ctx, job); err != nil {
				resultsMu.Lock()
				failed++
				resultsMu.Unlock()
				a.strictBatchFailed()
				return
			}
			resultsMu.Lock()
			succeeded++
			resultsMu.Unlock()
		}()
	}
	wg.Wait()
	return dispatched, succeeded, failed
}

func (a *SandboxActuator) strictBatchFailed() {
	a.mu.Lock()
	if a.strictOptimisticScale > 0 {
		a.strictOptimisticScale--
	}
	a.mu.Unlock()
}

func (a *SandboxActuator) StartOne(ctx context.Context, reason string) error {
	return a.startOne(ctx, reason)
}

func (a *SandboxActuator) StopAll(ctx context.Context) {
	a.mu.Lock()
	if a.targetInstances != 0 {
		a.targetRevision++
	}
	a.targetInstances = 0
	a.targetReason = ""
	count := a.totalInstances
	a.mu.Unlock()
	a.wakeScaleUp()
	_ = a.scaleDownParallel(ctx, count)
}

func (a *SandboxActuator) PublishCapacity() {
	a.publishCapacity()
}

// ReconcileWorkerState removes leaf-local instances that the worker no longer
// reports. This is the periodic relist / anti-entropy backstop for sandbox
// exits that happen outside the leaf's StopSandbox path. The caller builds one
// WorkerPresence per snapshot and shares it across actuators, keeping the cost
// O(instances) instead of O(functions x instances).
func (a *SandboxActuator) ReconcileWorkerState(workerIdx int, presence WorkerPresence) {
	if workerIdx < 0 {
		return
	}
	var removed []removedInstance
	a.mu.Lock()
	if workerIdx >= len(a.workerInstances) {
		a.mu.Unlock()
		return
	}
	ws := &a.workerInstances[workerIdx]
	kept := ws.instances[:0]
	for _, instance := range ws.instances {
		if _, ok := presence.InstanceIDs[instance.id]; ok {
			kept = append(kept, instance)
			continue
		}
		// Unrevisioned snapshots cannot prove ordering and are not safe eviction
		// evidence. Production workers always publish both revisions.
		if instance.workerStateRevision == 0 || presence.Revision == 0 {
			kept = append(kept, instance)
			continue
		}
		// A revisioned identity snapshot is authoritative only if it was
		// captured after this instance entered the worker's sandbox map.
		if presence.Revision < instance.workerStateRevision {
			kept = append(kept, instance)
			continue
		}
		removed = append(removed, removedInstance{
			id:             instance.id,
			address:        instance.address,
			createRevision: instance.workerStateRevision,
			stopping:       instance.stopping,
		})
		if instance.stopping && a.pendingStops > 0 {
			a.pendingStops--
		}
		a.totalInstances--
	}
	ws.instances = kept
	a.mu.Unlock()

	a.evictTracked(workerIdx, removed, 0, "relist")
}

// HandleSandboxRemoved applies one worker removal event. It is the low-latency
// fast path; ReconcileWorkerState repairs any event that is dropped or arrives
// before the leaf has tracked the instance. It reports whether the instance
// belonged to this function.
func (a *SandboxActuator) HandleSandboxRemoved(workerIdx int, instanceID, workerRevision uint64) bool {
	if workerIdx < 0 {
		return false
	}
	var removed []removedInstance
	a.mu.Lock()
	if workerIdx >= len(a.workerInstances) {
		a.mu.Unlock()
		return false
	}
	ws := &a.workerInstances[workerIdx]
	for i := range ws.instances {
		instance := ws.instances[i]
		if instance.id != instanceID {
			continue
		}
		if instance.workerStateRevision != 0 && workerRevision != 0 && workerRevision < instance.workerStateRevision {
			a.mu.Unlock()
			return false
		}
		ws.instances = append(ws.instances[:i], ws.instances[i+1:]...)
		a.totalInstances--
		removed = append(removed, removedInstance{
			id:             instance.id,
			address:        instance.address,
			createRevision: instance.workerStateRevision,
			stopping:       instance.stopping,
		})
		if instance.stopping && a.pendingStops > 0 {
			a.pendingStops--
		}
		break
	}
	a.mu.Unlock()

	a.evictTracked(workerIdx, removed, workerRevision, "removal event")
	return len(removed) > 0
}

// evictTracked applies the shared side effects of evicting instances from leaf
// routing, admission, placement, and capacity accounting.
func (a *SandboxActuator) evictTracked(workerIdx int, removed []removedInstance, workerRevision uint64, source string) {
	for _, instance := range removed {
		if a.placement != nil {
			a.placement.RecordStop(uint64(workerIdx + 1))
		}
		_ = a.store.RemoveInstance(context.Background(), instance.id)
		a.pool.Remove(instance.id)
		a.logger.Warn("removed sandbox missing from worker state",
			"worker_id", workerIdx+1,
			"instance_id", instance.id,
			"address", instance.address,
			"create_revision", instance.createRevision,
			"worker_revision", workerRevision,
			"source", source,
		)
	}
	if len(removed) > 0 {
		a.publishCapacity()
		a.wakeScaleUp()
	}
}

func (a *SandboxActuator) ReadyStats() (ready int, available uint64) {
	snapshot := a.pool.Snapshot()
	return snapshot.ReadyInstances, snapshot.AvailableConcurrency
}

func (a *SandboxActuator) AdmissionStats() dataplane.PoolSnapshot {
	return a.pool.Snapshot()
}

func (a *SandboxActuator) TakeAdmissionHighWater() int64 {
	return a.pool.TakeHighWater()
}

func (a *SandboxActuator) scaleDownParallel(ctx context.Context, count int) error {
	if count <= 0 {
		return nil
	}
	if delay := a.cfg.Dataplane.ScaleDownDelay; delay > 0 {
		a.mu.Lock()
		targetRevision := a.targetRevision
		a.mu.Unlock()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				goto delayComplete
			case <-a.scaleUpWake:
				a.mu.Lock()
				targetChanged := a.targetRevision != targetRevision
				a.mu.Unlock()
				if targetChanged {
					// Do not let a delayed downscale block the only lifecycle
					// pump. The pump will immediately re-evaluate the new target
					// and can dispatch scale-up work.
					return nil
				}
			}
		}
	}

delayComplete:
	// A newer target may have arrived while the downscale delay elapsed. Reserve
	// victims and re-read the target under the same lock so an old decision
	// cannot remove useful capacity.
	stopList := a.instancesForStopWithinTarget(count)
	return a.stopInstances(ctx, stopList)
}

func (a *SandboxActuator) stopInstances(ctx context.Context, stopList []stopItem) error {
	var wg sync.WaitGroup
	for _, item := range stopList {
		wg.Add(1)
		go func(item stopItem) {
			defer wg.Done()
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.Dataplane.StopTimeout)
			defer cancel()
			finishDrain, err := a.pool.Drain(stopCtx, item.instanceID)
			if err != nil {
				a.cancelStop(item.workerIdx, item.instanceID)
				a.logger.Warn("drain sandbox failed", "instance_id", item.instanceID, "error", err)
				return
			}
			if err := a.workers[item.workerIdx].StopSandbox(stopCtx, item.instanceID); err != nil {
				// Worker already tore the sandbox down (lifecycle race / double-stop).
				// Evict leaf state; do not re-admit a dead address into the lease pool.
				if isSandboxAlreadyGone(err) {
					finishDrain(true)
					a.removeInstance(item.workerIdx, item.instanceID)
					a.logger.Info("stopped sandbox", "worker_id", item.workerIdx+1, "instance_id", item.instanceID, "already_gone", true)
					return
				}
				finishDrain(false)
				a.cancelStop(item.workerIdx, item.instanceID)
				a.logger.Warn("stop sandbox failed", "instance_id", item.instanceID, "error", err)
				return
			}
			finishDrain(true)
			a.removeInstance(item.workerIdx, item.instanceID)
			a.logger.Info("stopped sandbox", "worker_id", item.workerIdx+1, "instance_id", item.instanceID)
		}(item)
	}
	wg.Wait()
	return nil
}

func (a *SandboxActuator) startOne(ctx context.Context, reason string) error {
	job, err := a.planStart(ctx, reason, false)
	if err != nil || job == nil {
		return err
	}
	return a.runStart(ctx, job)
}

type startJob struct {
	startAt     time.Time
	scheduledAt time.Time
	spec        *core.FunctionSpec
	reason      string
	reservation PlacementReservation
	worker      *leafworker.Client
	workerID    uint64
	instanceID  uint64
	timing      ScheduleTiming
}

func (a *SandboxActuator) planStart(ctx context.Context, reason string, enforceTarget bool) (*startJob, error) {
	startAt := time.Now()
	spec := a.currentSpec()
	var workers []*core.WorkerState
	demand := &core.ScaleDemand{FunctionId: a.functionID}
	var reservation PlacementReservation
	var decision PlacementChoice
	var timing ScheduleTiming
	var err error
	if a.placement != nil {
		decision, reservation, workers, timing, err = a.placement.Schedule(ctx, a.scheduler, spec, workers, demand)
	} else {
		workers = a.workerSnapshots()
		picked, pickErr := a.scheduler.PickWorker(ctx, spec, workers, demand)
		err = pickErr
		if picked != nil {
			decision = PlacementChoice{WorkerID: picked.GetWorkerId(), Reason: picked.GetReason()}
		}
	}
	if errors.Is(err, errStartNotNeeded) {
		return nil, nil
	}
	if errors.Is(err, ErrStartSlotsExhausted) {
		return nil, err
	}
	scheduledAt := time.Now()
	if err != nil || decision.WorkerID == 0 {
		schedulerReason := "scheduler error"
		if err != nil {
			schedulerReason = err.Error()
		} else if decision.Reason != "" {
			schedulerReason = decision.Reason
		}
		a.logger.Info("sandbox scheduling failed",
			"scale_reason", reason,
			"scheduler_reason", schedulerReason,
			"placement_lock_wait_ms", timing.LockWaitMs,
			"placement_hold_ms", timing.HoldMs,
			"placement_pick_ms", timing.PickMs,
			"worker_instances", workerInstanceCounts(workers),
			"worker_cold_starts", workerColdStartCounts(workers),
			"max_instances_per_worker", a.cfg.Dataplane.MaxInstancesPerWorker,
		)
		return nil, fmt.Errorf("no worker for scale: %s", schedulerReason)
	}
	a.logger.Info("sandbox scheduled",
		"worker_id", decision.WorkerID,
		"scale_reason", reason,
		"scheduler_reason", decision.Reason,
		"placement_lock_wait_ms", timing.LockWaitMs,
		"placement_hold_ms", timing.HoldMs,
		"placement_pick_ms", timing.PickMs,
		"worker_instances", workerInstanceCounts(workers),
		"worker_cold_starts", workerColdStartCounts(workers),
		"max_instances_per_worker", a.cfg.Dataplane.MaxInstancesPerWorker,
	)
	worker := a.workerByID(decision.WorkerID)
	if worker == nil {
		if reservation.Valid {
			a.placement.CancelReservation(reservation)
		}
		return nil, fmt.Errorf("worker %d not found", decision.WorkerID)
	}

	if err := a.reserveLocalStart(decision.WorkerID, spec, reason, enforceTarget); err != nil {
		if reservation.Valid {
			a.placement.CancelReservation(reservation)
		}
		if errors.Is(err, errStartNotNeeded) {
			return nil, nil
		}
		return nil, err
	}
	return &startJob{
		startAt: startAt, scheduledAt: scheduledAt, spec: spec, reason: reason,
		reservation: reservation, worker: worker, workerID: decision.WorkerID, instanceID: rand.Uint64(),
		timing: timing,
	}, nil
}

func (a *SandboxActuator) runStart(ctx context.Context, job *startJob) (err error) {
	var startedLocal bool
	defer func() {
		if !startedLocal {
			a.mu.Lock()
			a.pendingStarts--
			ws := &a.workerInstances[job.worker.Index()]
			ws.pendingStarts--
			a.mu.Unlock()
			if job.reservation.Valid {
				a.placement.CancelReservation(job.reservation)
			}
		}
		// Normal mode can dispatch a replacement only after both local and
		// placement counters are released. Strict batches ignore this wake.
		a.wakeScaleUp()
	}()

	prepareAt := time.Now()
	artifact, err := job.worker.PrepareImage(ctx, job.spec)
	if err != nil {
		a.logStartFailed(job, prepareAt, time.Time{}, time.Time{}, "prepare_image", err)
		return err
	}
	createAt := time.Now()
	req := &core.StartSandboxRequest{
		Function:   job.spec,
		Artifact:   artifact,
		WorkerId:   uint64(job.worker.Index() + 1),
		InstanceId: job.instanceID,
	}
	inst, err := job.worker.CreateSandbox(ctx, req)
	if err != nil {
		a.logStartFailed(job, prepareAt, createAt, time.Time{}, "create_sandbox", err)
		return err
	}
	storeAt := time.Now()
	inst.FunctionId = a.functionID
	inst.Protocol = job.spec.GetRuntime().GetProtocol()
	if job.spec.GetScale() != nil {
		inst.MaxConcurrency = job.spec.GetScale().GetMaxConcurrency()
	} else {
		inst.MaxConcurrency = 1
	}
	if inst.MaxConcurrency == 0 {
		inst.MaxConcurrency = dataplane.DefaultSandboxConcurrency
		inst.AvailableConcurrency = dataplane.DefaultSandboxConcurrency
	} else {
		inst.AvailableConcurrency = inst.MaxConcurrency
	}

	if err := a.store.PutInstance(ctx, inst); err != nil {
		a.logStartFailed(job, prepareAt, createAt, storeAt, "store_instance", err)
		return err
	}
	a.pool.Add(dataplane.Sandbox{
		InstanceID: inst.GetInstanceId(),
		WorkerID:   inst.GetWorkerId(),
		Address:    inst.GetAddress(),
		Protocol:   inst.GetProtocol(),
	})

	a.mu.Lock()
	startedLocal = true
	a.pendingStarts--
	ws := &a.workerInstances[job.worker.Index()]
	ws.pendingStarts--
	ws.instances = append(ws.instances, trackedInstance{
		id:                  inst.GetInstanceId(),
		address:             inst.GetAddress(),
		workerStateRevision: inst.GetWorkerStateRevision(),
	})
	a.totalInstances++
	stats := a.statsLocked()
	a.mu.Unlock()
	if job.reservation.Valid {
		a.placement.CommitReservation(job.reservation)
	}

	a.logger.Info("started sandbox", "instance_id", inst.GetInstanceId(), "address", inst.GetAddress(), "reason", job.reason)
	a.logger.Info("leaf sandbox start completed",
		"instance_id", inst.GetInstanceId(),
		"worker_id", job.workerID,
		"reason", job.reason,
		"total_ms", millisSince(job.startAt),
		"schedule_ms", millisBetween(job.startAt, job.scheduledAt),
		"placement_lock_wait_ms", job.timing.LockWaitMs,
		"placement_hold_ms", job.timing.HoldMs,
		"placement_pick_ms", job.timing.PickMs,
		"prepare_ms", millisBetween(prepareAt, createAt),
		"create_ms", millisBetween(createAt, storeAt),
		"store_ms", millisBetween(storeAt, time.Now()),
		"actual_instances", stats.ActualInstances,
		"pending_starts", stats.PendingStarts,
		"worker_instances", stats.WorkerInstances,
		"worker_pending_starts", stats.WorkerPending,
	)
	a.publishCapacity()
	return nil
}

func (a *SandboxActuator) logStartFailed(job *startJob, prepareAt, createAt, storeAt time.Time, phase string, err error) {
	now := time.Now()
	prepareMs := int64(0)
	createMs := int64(0)
	storeMs := int64(0)
	if !prepareAt.IsZero() {
		switch phase {
		case "prepare_image":
			prepareMs = millisBetween(prepareAt, now)
		default:
			prepareMs = millisBetween(prepareAt, createAt)
		}
	}
	if !createAt.IsZero() {
		switch phase {
		case "create_sandbox":
			createMs = millisBetween(createAt, now)
		default:
			createMs = millisBetween(createAt, storeAt)
		}
	}
	if phase == "store_instance" && !storeAt.IsZero() {
		storeMs = millisBetween(storeAt, now)
	}
	stats := a.Stats()
	a.logger.Warn("leaf sandbox start failed",
		"instance_id", job.instanceID,
		"worker_id", job.workerID,
		"reason", job.reason,
		"phase", phase,
		"error", err,
		"total_ms", millisSince(job.startAt),
		"schedule_ms", millisBetween(job.startAt, job.scheduledAt),
		"placement_lock_wait_ms", job.timing.LockWaitMs,
		"placement_hold_ms", job.timing.HoldMs,
		"placement_pick_ms", job.timing.PickMs,
		"prepare_ms", prepareMs,
		"create_ms", createMs,
		"store_ms", storeMs,
		"actual_instances", stats.ActualInstances,
		"pending_starts", stats.PendingStarts,
		"worker_instances", stats.WorkerInstances,
		"worker_pending_starts", stats.WorkerPending,
	)
}

func (a *SandboxActuator) maxStartsPerReconcile() int {
	if a.cfg.Dataplane.DirigentStrictAdmission {
		return maxConcurrentStarts
	}
	if max := a.cfg.Dataplane.MaxStartsPerReconcile; max > 0 {
		return int(max)
	}
	perWorker := int(a.cfg.Dataplane.StartTokensPerWorker)
	if perWorker == 0 {
		perWorker = defaultStartBurstPerWorker
	}
	return len(a.workers) * perWorker
}

// scalePump is the only normal-mode owner of sandbox starts and stops. Target
// updates only wake this loop; they never dispatch a second lifecycle batch.
func (a *SandboxActuator) scalePump(ctx context.Context) {
	defer func() {
		a.mu.Lock()
		a.scaleUpActive = false
		a.mu.Unlock()
	}()

	for {
		a.mu.Lock()
		excess := a.totalInstances + a.pendingStarts - a.pendingStops - a.targetInstances
		a.mu.Unlock()
		if excess > 0 {
			_ = a.scaleDownParallel(ctx, excess)
		}

		for {
			a.mu.Lock()
			gap := a.targetInstances - (a.totalInstances + a.pendingStarts - a.pendingStops)
			reason := a.targetReason
			startSlotsAvailable := a.placement != nil || a.pendingStarts < a.maxStartsPerReconcile()
			desired := a.targetInstances
			actual := a.totalInstances
			pending := a.pendingStarts
			pendingStops := a.pendingStops
			a.mu.Unlock()
			if gap <= 0 || !startSlotsAvailable || reason == "" {
				break
			}
			if a.cfg.Dataplane.DirigentStrictAdmission && pending == 0 {
				a.logger.Info("Dirigent-copy scale-up dispatch",
					"desired_instances", desired,
					"actual_instances", actual,
					"pending_starts", pending,
					"pending_stops", pendingStops,
					"start_gap", gap,
					"reason", reason,
				)
			}

			job, err := a.planStart(ctx, reason, true)
			if errors.Is(err, ErrStartSlotsExhausted) || errors.Is(err, errStartNotNeeded) {
				break
			}
			if err != nil {
				a.logger.Warn("scale up dispatch failed", "error", err)
				break
			}
			if job == nil {
				break
			}
			go func() {
				if err := a.runStart(ctx, job); err != nil && ctx.Err() == nil {
					a.logger.Warn("scale up failed", "error", err)
				}
			}()
		}

		waitStart := time.Now()
		select {
		case <-ctx.Done():
			return
		case <-a.scaleUpWake:
			a.logger.Info("scale_up_wait",
				"function_id", a.functionID,
				"wait_ms", time.Since(waitStart).Milliseconds(),
				"reason", "wake",
			)
		case <-a.startCapacityChanged():
			a.logger.Info("scale_up_wait",
				"function_id", a.functionID,
				"wait_ms", time.Since(waitStart).Milliseconds(),
				"reason", "start_capacity",
			)
		}
	}
}

func (a *SandboxActuator) startCapacityChanged() <-chan struct{} {
	if notifier, ok := a.placement.(placementStartCapacityNotifier); ok {
		return notifier.StartCapacityChanged()
	}
	return nil
}

func (a *SandboxActuator) wakeScaleUp() {
	if a.scaleUpWake == nil {
		return
	}
	select {
	case a.scaleUpWake <- struct{}{}:
	default:
	}
}

type stopItem struct {
	workerIdx  int
	instanceID uint64
	address    string
}

func (a *SandboxActuator) instancesForStop(count int) []stopItem {
	if count <= 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.instancesForStopLocked(count)
}

func (a *SandboxActuator) instancesForStopWithinTarget(count int) []stopItem {
	if count <= 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	excess := a.totalInstances + a.pendingStarts - a.pendingStops - a.targetInstances
	if excess <= 0 {
		return nil
	}
	if count > excess {
		count = excess
	}
	return a.instancesForStopLocked(count)
}

func (a *SandboxActuator) instancesForStopLocked(count int) []stopItem {
	out := make([]stopItem, 0, count)
	for idx := range a.workerInstances {
		for i := range a.workerInstances[idx].instances {
			inst := &a.workerInstances[idx].instances[i]
			if inst.stopping {
				continue
			}
			inst.stopping = true
			a.pendingStops++
			out = append(out, stopItem{workerIdx: idx, instanceID: inst.id, address: inst.address})
			if len(out) >= count {
				return out
			}
		}
	}
	return out
}

func (a *SandboxActuator) cancelStop(workerIdx int, instanceID uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if workerIdx < 0 || workerIdx >= len(a.workerInstances) {
		return
	}
	for i := range a.workerInstances[workerIdx].instances {
		if a.workerInstances[workerIdx].instances[i].id == instanceID {
			if a.workerInstances[workerIdx].instances[i].stopping {
				a.workerInstances[workerIdx].instances[i].stopping = false
				if a.pendingStops > 0 {
					a.pendingStops--
				}
			}
			return
		}
	}
}

// isSandboxAlreadyGone reports whether StopSandbox failed because the worker
// no longer has the sandbox (lifecycle race or prior stop). Leaf must evict
// local state rather than re-admitting the dead address into the lease pool.
func isSandboxAlreadyGone(err error) bool {
	return status.Code(err) == codes.NotFound
}

func (a *SandboxActuator) removeInstance(workerIdx int, instanceID uint64) {
	var address string
	a.mu.Lock()
	if workerIdx < 0 || workerIdx >= len(a.workerInstances) {
		a.mu.Unlock()
		return
	}
	ws := &a.workerInstances[workerIdx]
	for i, inst := range ws.instances {
		if inst.id == instanceID {
			address = inst.address
			if inst.stopping && a.pendingStops > 0 {
				a.pendingStops--
			}
			last := len(ws.instances) - 1
			ws.instances[i] = ws.instances[last]
			ws.instances = ws.instances[:last]
			a.totalInstances--
			break
		}
	}
	a.mu.Unlock()
	if address == "" {
		return
	}
	if a.placement != nil {
		a.placement.RecordStop(uint64(workerIdx + 1))
	}
	_ = a.store.RemoveInstance(context.Background(), instanceID)
	a.pool.Remove(instanceID)
	a.publishCapacity()
	a.wakeScaleUp()
}

func (a *SandboxActuator) workerSnapshots() []*core.WorkerState {
	a.mu.Lock()
	defer a.mu.Unlock()
	telemetry := a.latestWorkerStates()
	out := make([]*core.WorkerState, len(a.workers))
	for i := range a.workers {
		// Worker telemetry can lag behind lifecycle RPCs, so overlay leaf-local
		// actual instances. When a placement coordinator is configured it overlays
		// pending starts globally across functions; otherwise keep the legacy
		// function-local pending overlay here.
		instances := len(a.workerInstances[i].instances)
		if a.placement == nil {
			instances += a.workerInstances[i].pendingStarts
		}
		state := &core.WorkerState{
			WorkerId:    uint64(i + 1),
			Schedulable: true,
			Healthy:     true,
			Instances:   uint64(instances),
		}
		if i < len(telemetry) && telemetry[i] != nil {
			state = proto.Clone(telemetry[i]).(*core.WorkerState)
			state.WorkerId = uint64(i + 1)
			if state.Instances < uint64(instances) {
				state.Instances = uint64(instances)
			}
		}
		if a.placement == nil && a.workerInstances[i].pendingStarts >= a.startLimitPerWorker() {
			state.Schedulable = false
		}
		out[i] = state
	}
	return out
}

var (
	errStartNotNeeded = errors.New("start not needed")
	// ErrStartSlotsExhausted is temporary; dispatchers should wait for a completion or target update.
	ErrStartSlotsExhausted = errors.New("start slots exhausted")
)

func (a *SandboxActuator) reserveLocalStart(workerID uint64, spec *core.FunctionSpec, reason string, enforceTarget bool) error {
	worker := a.workerByID(workerID)
	if worker == nil {
		return fmt.Errorf("worker %d not found", workerID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ws := &a.workerInstances[worker.Index()]
	if a.placement == nil {
		if ws.pendingStarts >= a.startLimitPerWorker() || a.pendingStarts >= a.maxStartsPerReconcile() {
			return ErrStartSlotsExhausted
		}
		if leaf.InstancesPerWorkerAtCap(len(ws.instances)+ws.pendingStarts, a.cfg.Dataplane.MaxInstancesPerWorker) {
			return fmt.Errorf("worker at capacity")
		}
	}
	future := a.totalInstances + a.pendingStarts - a.pendingStops
	if enforceTarget && future >= a.targetInstances {
		return errStartNotNeeded
	}
	if reason == "cold-start" && future >= 1 {
		return errStartNotNeeded
	}
	if scale := spec.GetScale(); scale != nil && scale.GetMaxInstances() > 0 && future >= int(scale.GetMaxInstances()) {
		return fmt.Errorf("function at max_instances")
	}
	a.pendingStarts++
	ws.pendingStarts++
	return nil
}

func (a *SandboxActuator) startLimitPerWorker() int {
	if a.cfg.Dataplane.DirigentStrictAdmission {
		return maxConcurrentStarts
	}
	if limit := int(a.cfg.Dataplane.StartTokensPerWorker); limit > 0 {
		return limit
	}
	return defaultStartBurstPerWorker
}

const maxConcurrentStarts = int(^uint(0) >> 1)

func (a *SandboxActuator) latestWorkerStates() []*core.WorkerState {
	if a.workerState == nil {
		return nil
	}
	return a.workerState()
}

func workerInstanceCounts(workers []*core.WorkerState) []uint64 {
	out := make([]uint64, len(workers))
	for i, worker := range workers {
		out[i] = worker.GetInstances()
	}
	return out
}

func workerColdStartCounts(workers []*core.WorkerState) []uint64 {
	out := make([]uint64, len(workers))
	for i, worker := range workers {
		out[i] = uint64(worker.GetColdStartsInFlight())
	}
	return out
}

func (a *SandboxActuator) statsLocked() ActuatorStats {
	stats := ActuatorStats{
		ActualInstances: a.totalInstances,
		PendingStarts:   a.pendingStarts,
		PendingStops:    a.pendingStops,
		WorkerInstances: make([]uint64, len(a.workerInstances)),
		WorkerPending:   make([]uint64, len(a.workerInstances)),
	}
	for i := range a.workerInstances {
		stats.WorkerInstances[i] = uint64(len(a.workerInstances[i].instances))
		stats.WorkerPending[i] = uint64(a.workerInstances[i].pendingStarts)
	}
	return stats
}

func millisSince(start time.Time) int64 {
	return int64(time.Since(start) / time.Millisecond)
}

func millisBetween(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return int64(end.Sub(start) / time.Millisecond)
}

func (a *SandboxActuator) workerByID(workerID uint64) *leafworker.Client {
	for _, w := range a.workers {
		if uint64(w.Index()+1) == workerID {
			return w
		}
	}
	return nil
}

func (a *SandboxActuator) currentSpec() *core.FunctionSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.spec
}

func (a *SandboxActuator) publishCapacity() {
	snapshot := a.pool.Snapshot()
	ready := uint32(snapshot.ReadyInstances)
	status := core.CapacityStatus_CAPACITY_STATUS_COLD
	if ready > 0 {
		status = core.CapacityStatus_CAPACITY_STATUS_AVAILABLE
	}
	a.reporter.Publish(&core.FunctionCapacity{
		FunctionId:           a.functionID,
		ReadyInstances:       ready,
		AvailableConcurrency: snapshot.AvailableConcurrency,
		InFlight:             uint64(snapshot.Executing),
		Status:               status,
		ObservedAt:           timestamppb.Now(),
	})
}
