package autoscaling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
	"hyperfaas-ideal-arch/pkg/workerpb"
)

func testPoolFromStore(store *dataplane.Store, functionID uint64) *dataplane.SandboxPool {
	pool := dataplane.NewSandboxPool(1, 1)
	for _, instance := range store.ListReady(functionID) {
		pool.Add(dataplane.Sandbox{
			InstanceID: instance.GetInstanceId(),
			WorkerID:   instance.GetWorkerId(),
			Address:    instance.GetAddress(),
			Protocol:   instance.GetProtocol(),
		})
	}
	return pool
}

func TestMaxStartsPerReconcileUsesExplicitCap(t *testing.T) {
	a := &SandboxActuator{
		cfg: leaf.LeafConfig{
			Dataplane: leaf.DataplaneConfig{
				MaxStartsPerReconcile: 16,
				StartTokensPerWorker:  4,
			},
		},
		workers: make([]*leafworker.Client, 4),
	}
	if got := a.maxStartsPerReconcile(); got != 16 {
		t.Fatalf("maxStartsPerReconcile()=%d, want 16", got)
	}
}

func TestDirigentAdmissionBypassesStartCaps(t *testing.T) {
	a := &SandboxActuator{
		cfg: leaf.LeafConfig{
			Dataplane: leaf.DataplaneConfig{
				DirigentStrictAdmission: true,
				MaxStartsPerReconcile:   1,
				StartTokensPerWorker:    1,
			},
		},
		workers: make([]*leafworker.Client, 4),
	}
	if got := a.maxStartsPerReconcile(); got != maxConcurrentStarts {
		t.Fatalf("maxStartsPerReconcile()=%d, want unbounded Dirigent dispatch", got)
	}
	if got := a.startLimitPerWorker(); got != maxConcurrentStarts {
		t.Fatalf("startLimitPerWorker()=%d, want unbounded Dirigent dispatch", got)
	}
}

type stopRecordingWorker struct {
	workerpb.UnimplementedSandboxServiceServer
	stopped chan uint64
}

func (w *stopRecordingWorker) StopSandbox(_ context.Context, req *workerpb.StopSandboxRequest) (*workerpb.StopSandboxResponse, error) {
	w.stopped <- req.GetInstanceId()
	return &workerpb.StopSandboxResponse{}, nil
}

type discardPublisher struct{}

func (discardPublisher) Publish(*core.FunctionCapacity) {}

func TestReconcileWorkerStateRemovesSandboxAfterConfirmedAbsence(t *testing.T) {
	const (
		functionID = 7
		instanceID = 701
		address    = "127.0.0.1:1701"
	)
	store := dataplane.NewStore()
	if err := store.PutInstance(context.Background(), &core.InstanceState{
		FunctionId: functionID,
		InstanceId: instanceID,
		WorkerId:   1,
		Address:    address,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	pool := testPoolFromStore(store, functionID)
	pool.Add(dataplane.Sandbox{
		InstanceID: instanceID,
		WorkerID:   1,
		Address:    address,
	})
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      store,
		Pool:       pool,
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
		id: instanceID, address: address, workerStateRevision: 7,
	})
	actuator.totalInstances = 1

	absent := NewWorkerPresence(&core.WorkerState{WorkerId: 1, Healthy: true, Schedulable: true, SandboxRevision: 8})
	actuator.ReconcileWorkerState(0, absent)
	if got := actuator.ActualInstances(); got != 0 {
		t.Fatalf("actual instances after confirmed absence = %d, want 0", got)
	}
	if got := len(store.ListReady(functionID)); got != 0 {
		t.Fatalf("ready instances after confirmed absence = %d, want 0", got)
	}
}

func TestReconcileWorkerStateDoesNotTrustUnrevisionedAbsence(t *testing.T) {
	const (
		functionID = 8
		instanceID = 801
	)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{id: instanceID})
	actuator.totalInstances = 1
	absent := NewWorkerPresence(&core.WorkerState{WorkerId: 1})
	for range 3 {
		actuator.ReconcileWorkerState(0, absent)
	}
	if got := actuator.ActualInstances(); got != 1 {
		t.Fatalf("actual instances after unrevisioned absences = %d, want 1", got)
	}
}

func TestReconcileWorkerStateIgnoresSnapshotOlderThanCreateRevision(t *testing.T) {
	const (
		functionID = 10
		instanceID = 1001
	)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
		id:                  instanceID,
		workerStateRevision: 12,
	})
	actuator.totalInstances = 1

	for range 3 {
		actuator.ReconcileWorkerState(0, NewWorkerPresence(&core.WorkerState{SandboxRevision: 11}))
	}
	if got := actuator.ActualInstances(); got != 1 {
		t.Fatalf("actual instances after stale snapshots = %d, want 1", got)
	}

	actuator.ReconcileWorkerState(0, NewWorkerPresence(&core.WorkerState{SandboxRevision: 12}))
	if got := actuator.ActualInstances(); got != 0 {
		t.Fatalf("actual instances after authoritative absence = %d, want 0", got)
	}
	// A concurrent normal stop completion after reconciliation is a no-op.
	actuator.removeInstance(0, instanceID)
	if got := actuator.ActualInstances(); got != 0 {
		t.Fatalf("actual instances after duplicate removal = %d, want 0", got)
	}
}

func TestReconcileWorkerStateAcceptsOutOfOrderPresentSnapshot(t *testing.T) {
	const (
		functionID = 11
		instanceID = 1101
	)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
		id:                  instanceID,
		workerStateRevision: 20,
	})
	actuator.totalInstances = 1

	actuator.ReconcileWorkerState(0, NewWorkerPresence(&core.WorkerState{
		SandboxRevision: 19,
		SandboxStates: []*core.InstanceState{{
			FunctionId: functionID,
			InstanceId: instanceID,
		}},
	}))
	actuator.ReconcileWorkerState(0, NewWorkerPresence(&core.WorkerState{SandboxRevision: 19}))
	if got := actuator.ActualInstances(); got != 1 {
		t.Fatalf("actual instances after out-of-order snapshots = %d, want 1", got)
	}
}

func TestReconcileWorkerStateOnlyChangesSelectedWorker(t *testing.T) {
	const functionID = 9
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 2),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{id: 901, workerStateRevision: 1})
	actuator.workerInstances[1].instances = append(actuator.workerInstances[1].instances, trackedInstance{id: 902, workerStateRevision: 1})
	actuator.totalInstances = 2

	actuator.ReconcileWorkerState(0, NewWorkerPresence(&core.WorkerState{WorkerId: 1, SandboxRevision: 2}))
	stats := actuator.Stats()
	if stats.ActualInstances != 1 || stats.WorkerInstances[0] != 0 || stats.WorkerInstances[1] != 1 {
		t.Fatalf("stats after worker 1 absence = %+v, want only worker 2 instance", stats)
	}
}

func TestHandleSandboxRemovedEvictsTrackedInstance(t *testing.T) {
	const (
		functionID = 12
		instanceID = 1201
		address    = "127.0.0.1:1811"
	)
	store := dataplane.NewStore()
	if err := store.PutInstance(context.Background(), &core.InstanceState{
		FunctionId: functionID,
		InstanceId: instanceID,
		WorkerId:   1,
		Address:    address,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	pool := testPoolFromStore(store, functionID)
	pool.Add(dataplane.Sandbox{
		InstanceID: instanceID,
		WorkerID:   1,
		Address:    address,
	})
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      store,
		Pool:       pool,
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
		id: instanceID, address: address, workerStateRevision: 5,
	})
	actuator.totalInstances = 1

	if !actuator.HandleSandboxRemoved(0, instanceID, 6) {
		t.Fatal("HandleSandboxRemoved = false, want true")
	}
	if got := actuator.ActualInstances(); got != 0 {
		t.Fatalf("actual instances after removal event = %d, want 0", got)
	}
	if got := len(store.ListReady(functionID)); got != 0 {
		t.Fatalf("ready instances after removal event = %d, want 0", got)
	}
	// A duplicate delivery after the instance is gone is a no-op.
	if actuator.HandleSandboxRemoved(0, instanceID, 7) {
		t.Fatal("duplicate removal event reported an owner")
	}
}

func TestHandleSandboxRemovedRejectsStaleRevision(t *testing.T) {
	const (
		functionID = 13
		instanceID = 1301
	)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
		id:                  instanceID,
		workerStateRevision: 9,
	})
	actuator.totalInstances = 1

	if actuator.HandleSandboxRemoved(0, instanceID, 8) {
		t.Fatal("stale removal event was applied")
	}
	if got := actuator.ActualInstances(); got != 1 {
		t.Fatalf("actual instances after stale event = %d, want 1", got)
	}
}

func TestPendingStopsPreventScaleDownOvershoot(t *testing.T) {
	const target = 19
	a := NewSandboxActuator(ActuatorConfig{
		FunctionID: 1,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    make([]*leafworker.Client, 1),
		Store:      dataplane.NewStore(),
		Pool:       dataplane.NewSandboxPool(1, 1),
		Reporter:   discardPublisher{},
	})
	for id := uint64(1); id <= 23; id++ {
		a.workerInstances[0].instances = append(a.workerInstances[0].instances, trackedInstance{id: id})
	}
	a.totalInstances = 23
	a.pendingStarts = 2
	a.targetInstances = target

	// Future scale is 25, so exactly six stops are needed.
	stops := a.instancesForStop(6)
	if len(stops) != 6 || a.pendingStops != 6 {
		t.Fatalf("stops=%d pendingStops=%d, want 6/6", len(stops), a.pendingStops)
	}

	// A worker event for one selected victim wakes the controller in production.
	// The remaining five pending stops still account for the complete target gap.
	if !a.HandleSandboxRemoved(0, stops[0].instanceID, 1) {
		t.Fatal("selected stop was not removed")
	}
	future := a.totalInstances + a.pendingStarts - a.pendingStops
	if future != target {
		t.Fatalf("future scale=%d, want %d", future, target)
	}
	if extra := a.instancesForStopWithinTarget(6); len(extra) != 0 {
		t.Fatalf("controller selected %d extra stops after removal wake", len(extra))
	}
}

func TestScaleDownWaitsForActiveLease(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	workerService := &stopRecordingWorker{stopped: make(chan uint64, 1)}
	workerpb.RegisterSandboxServiceServer(server, workerService)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	workerClient, err := leafworker.NewClient(context.Background(), 0, listener.Addr().String(), time.Second, time.Second, time.Second, slog.Default())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer workerClient.Close()

	const functionID = 1
	const instanceID = 101
	const address = "127.0.0.1:50052"
	spec := &core.FunctionSpec{FunctionId: functionID, Scale: &core.ScalePolicySpec{MaxConcurrency: 1, MaxQueueDepth: 1}}
	store := dataplane.NewStore()
	if err := store.PutInstance(context.Background(), &core.InstanceState{
		FunctionId: functionID,
		InstanceId: instanceID,
		Address:    address,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	pool := testPoolFromStore(store, functionID)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}

	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Spec:       spec,
		LeafCfg:    leaf.LeafConfig{Dataplane: leaf.DataplaneConfig{StopTimeout: time.Second}},
		Logger:     slog.Default(),
		Workers:    []*leafworker.Client{workerClient},
		Store:      store,
		Pool:       pool,
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{id: instanceID, address: address})
	actuator.totalInstances = 1

	done := make(chan error, 1)
	go func() { done <- actuator.scaleDownParallel(context.Background(), 1) }()
	select {
	case id := <-workerService.stopped:
		t.Fatalf("worker stopped leased instance %d", id)
	case <-time.After(50 * time.Millisecond):
	}

	lease.Release()
	select {
	case id := <-workerService.stopped:
		if id != instanceID {
			t.Fatalf("stopped instance = %d, want %d", id, instanceID)
		}
	case <-time.After(time.Second):
		t.Fatal("worker stop did not run after lease release")
	}
	if err := <-done; err != nil {
		t.Fatalf("scaleDownParallel: %v", err)
	}
}

func TestScaleDownDelayYieldsToNewTarget(t *testing.T) {
	a := NewSandboxActuator(ActuatorConfig{
		LeafCfg: leaf.LeafConfig{Dataplane: leaf.DataplaneConfig{
			ScaleDownDelay: time.Second,
		}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  dataplane.NewStore(),
		Pool:   dataplane.NewSandboxPool(1, 1),
	})
	a.totalInstances = 10
	a.targetInstances = 5
	a.targetRevision = 1

	done := make(chan error, 1)
	go func() { done <- a.scaleDownParallel(context.Background(), 5) }()
	time.Sleep(20 * time.Millisecond)

	a.mu.Lock()
	a.targetInstances = 10
	a.targetRevision++
	a.mu.Unlock()
	a.wakeScaleUp()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("scaleDownParallel: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("delayed downscale blocked after a new target")
	}
	if got := a.Stats().PendingStops; got != 0 {
		t.Fatalf("pending stops = %d, want 0", got)
	}
}

type notFoundStopWorker struct {
	workerpb.UnimplementedSandboxServiceServer
	stopped chan uint64
}

func (w *notFoundStopWorker) StopSandbox(_ context.Context, req *workerpb.StopSandboxRequest) (*workerpb.StopSandboxResponse, error) {
	w.stopped <- req.GetInstanceId()
	return nil, status.Errorf(codes.NotFound, "sandbox %d not found", req.GetInstanceId())
}

func TestScaleDownNotFoundEvictsFromLeasePool(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	workerService := &notFoundStopWorker{stopped: make(chan uint64, 1)}
	workerpb.RegisterSandboxServiceServer(server, workerService)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	workerClient, err := leafworker.NewClient(context.Background(), 0, listener.Addr().String(), time.Second, time.Second, time.Second, slog.Default())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer workerClient.Close()

	const functionID = 1
	const instanceID = 202
	const address = "127.0.0.1:50053"
	spec := &core.FunctionSpec{FunctionId: functionID, Scale: &core.ScalePolicySpec{MaxConcurrency: 1, MaxQueueDepth: 1}}
	store := dataplane.NewStore()
	if err := store.PutInstance(context.Background(), &core.InstanceState{
		FunctionId: functionID,
		InstanceId: instanceID,
		Address:    address,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	pool := testPoolFromStore(store, functionID)

	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Spec:       spec,
		LeafCfg:    leaf.LeafConfig{Dataplane: leaf.DataplaneConfig{StopTimeout: time.Second}},
		Logger:     slog.Default(),
		Workers:    []*leafworker.Client{workerClient},
		Store:      store,
		Pool:       pool,
		Reporter:   discardPublisher{},
	})
	actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{id: instanceID, address: address})
	actuator.totalInstances = 1

	if err := actuator.scaleDownParallel(context.Background(), 1); err != nil {
		t.Fatalf("scaleDownParallel: %v", err)
	}
	select {
	case id := <-workerService.stopped:
		if id != instanceID {
			t.Fatalf("stopped instance = %d, want %d", id, instanceID)
		}
	default:
		t.Fatal("expected StopSandbox to be called")
	}

	if got := store.ListReady(functionID); len(got) != 0 {
		t.Fatalf("store still has %d ready instances after NotFound stop", len(got))
	}
	if got := pool.Snapshot().ReadyInstances; got != 0 {
		t.Fatalf("pool instance count = %d, want 0 (dead address must not be re-admitted)", got)
	}
	if got := actuator.Stats().ActualInstances; got != 0 {
		t.Fatalf("actuator actual instances = %d, want 0", got)
	}

	leaseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	lease, err := pool.Acquire(leaseCtx)
	if err == nil {
		addr := lease.Sandbox().Address
		lease.Release()
		t.Fatalf("Lease returned address %q after NotFound stop; want error", addr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire error = %v, want deadline exceeded", err)
	}
}

func TestMaxStartsPerReconcileUsesStartTokens(t *testing.T) {
	a := &SandboxActuator{
		cfg: leaf.LeafConfig{
			Dataplane: leaf.DataplaneConfig{
				StartTokensPerWorker: 4,
			},
		},
		workers: make([]*leafworker.Client, 4),
	}
	if got := a.maxStartsPerReconcile(); got != 16 {
		t.Fatalf("maxStartsPerReconcile()=%d, want 16", got)
	}
}

func TestMaxStartsPerReconcileDefaultsWhenUnlimited(t *testing.T) {
	a := &SandboxActuator{
		cfg:     leaf.LeafConfig{},
		workers: make([]*leafworker.Client, 4),
	}
	if got := a.maxStartsPerReconcile(); got != 4*defaultStartBurstPerWorker {
		t.Fatalf("maxStartsPerReconcile()=%d, want %d", got, 4*defaultStartBurstPerWorker)
	}
}

func TestDirigentAdmissionAllowsBatchPendingStarts(t *testing.T) {
	a := dirigentAdmissionActuator(true)
	spec := &core.FunctionSpec{}

	if err := a.reserveLocalStart(1, spec, "cold-start-panic", false); err != nil {
		t.Fatalf("first reserveLocalStart() error=%v, want nil", err)
	}
	if err := a.reserveLocalStart(1, spec, "cold-start-panic", false); err != nil {
		t.Fatalf("second reserveLocalStart() error=%v, want nil", err)
	}
	if got := a.Stats().PendingStarts; got != 2 {
		t.Fatalf("pending starts=%d, want 2", got)
	}
}

func TestDirigentAdmissionStillHonorsWorkerInstanceCap(t *testing.T) {
	a := dirigentAdmissionActuator(true)
	a.cfg.Dataplane.MaxInstancesPerWorker = 1
	a.workerInstances[0].instances = append(a.workerInstances[0].instances, trackedInstance{})

	err := a.reserveLocalStart(1, &core.FunctionSpec{}, "dirigent-optimistic", false)
	if err == nil || err.Error() != "worker at capacity" {
		t.Fatalf("reserveLocalStart() error=%v, want worker capacity rejection", err)
	}
}

func TestLegacyColdStartAdmissionStillBlocksDuplicateStarts(t *testing.T) {
	a := dirigentAdmissionActuator(false)
	spec := &core.FunctionSpec{}

	if err := a.reserveLocalStart(1, spec, "cold-start", false); err != nil {
		t.Fatalf("first reserveLocalStart() error=%v, want nil", err)
	}
	if err := a.reserveLocalStart(1, spec, "cold-start", false); !errors.Is(err, errStartNotNeeded) {
		t.Fatalf("second reserveLocalStart() error=%v, want %v", err, errStartNotNeeded)
	}
}

type configuredPlacement struct{}

func (*configuredPlacement) Schedule(context.Context, PlacementScheduler, *core.FunctionSpec, []*core.WorkerState, *core.ScaleDemand) (PlacementChoice, PlacementReservation, []*core.WorkerState, ScheduleTiming, error) {
	return PlacementChoice{}, PlacementReservation{}, nil, ScheduleTiming{}, nil
}

func (*configuredPlacement) CommitReservation(PlacementReservation) {}
func (*configuredPlacement) CancelReservation(PlacementReservation) {}
func (*configuredPlacement) RecordStop(uint64)                      {}

func TestPlacementOwnsTokenAdmission(t *testing.T) {
	a := dirigentAdmissionActuator(false)
	a.placement = &configuredPlacement{}
	a.cfg.Dataplane.StartTokensPerWorker = 1
	a.cfg.Dataplane.MaxStartsPerReconcile = 1
	a.cfg.Dataplane.MaxInstancesPerWorker = 1
	a.pendingStarts = 1
	a.workerInstances[0].pendingStarts = 1
	a.workerInstances[0].instances = append(a.workerInstances[0].instances, trackedInstance{})

	if err := a.reserveLocalStart(1, &core.FunctionSpec{}, "scale", false); err != nil {
		t.Fatalf("reserveLocalStart() enforced placement-owned admission: %v", err)
	}
	if got := a.Stats().PendingStarts; got != 2 {
		t.Fatalf("function-local pending starts=%d, want 2", got)
	}
}

func TestPlacementConfiguredActuatorKeepsFunctionAdmission(t *testing.T) {
	tests := []struct {
		name          string
		spec          *core.FunctionSpec
		reason        string
		enforceTarget bool
		configure     func(*SandboxActuator)
	}{
		{name: "target", spec: &core.FunctionSpec{}, reason: "scale", enforceTarget: true},
		{name: "cold start", spec: &core.FunctionSpec{}, reason: "cold-start", configure: func(a *SandboxActuator) { a.pendingStarts = 1 }},
		{name: "function maximum", spec: &core.FunctionSpec{Scale: &core.ScalePolicySpec{MaxInstances: 1}}, reason: "scale", configure: func(a *SandboxActuator) { a.totalInstances = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := dirigentAdmissionActuator(false)
			a.placement = &configuredPlacement{}
			if test.configure != nil {
				test.configure(a)
			}
			if err := a.reserveLocalStart(1, test.spec, test.reason, test.enforceTarget); err == nil {
				t.Fatal("reserveLocalStart() error=nil, want function admission rejection")
			}
		})
	}
}

func dirigentAdmissionActuator(enabled bool) *SandboxActuator {
	return &SandboxActuator{
		cfg: leaf.LeafConfig{
			Dataplane: leaf.DataplaneConfig{DirigentStrictAdmission: enabled},
		},
		workers:         []*leafworker.Client{{}},
		workerInstances: make([]workerInstanceState, 1),
	}
}

type startTracker struct {
	mu           sync.Mutex
	starts       int
	inFlight     int
	maxInFlight  int
	perWorker    []int
	maxPerWorker []int
	started      chan struct{}
	release      chan struct{}
}

func newStartTracker(workers int) *startTracker {
	return &startTracker{
		perWorker:    make([]int, workers),
		maxPerWorker: make([]int, workers),
		started:      make(chan struct{}, 256),
		release:      make(chan struct{}, 256),
	}
}

func (s *startTracker) begin(worker int) {
	s.mu.Lock()
	s.starts++
	s.inFlight++
	s.perWorker[worker]++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	if s.perWorker[worker] > s.maxPerWorker[worker] {
		s.maxPerWorker[worker] = s.perWorker[worker]
	}
	s.mu.Unlock()
	s.started <- struct{}{}
}

func (s *startTracker) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

func (s *startTracker) end(worker int) {
	s.mu.Lock()
	s.inFlight--
	s.perWorker[worker]--
	s.mu.Unlock()
}

func (s *startTracker) snapshot() (int, int, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight, s.maxInFlight, append([]int(nil), s.maxPerWorker...)
}

type blockingStartWorker struct {
	workerpb.UnimplementedSandboxServiceServer
	index       int
	tracker     *startTracker
	failCreates bool
	stopStarted chan struct{}
	stopRelease <-chan struct{}
}

func (w *blockingStartWorker) PrepareImage(_ context.Context, req *workerpb.PrepareImageRequest) (*core.PreparedArtifact, error) {
	return &core.PreparedArtifact{FunctionId: req.GetFunction().GetFunctionId()}, nil
}

func (w *blockingStartWorker) CreateSandbox(ctx context.Context, req *workerpb.CreateSandboxRequest) (*core.InstanceState, error) {
	w.tracker.begin(w.index)
	defer w.tracker.end(w.index)
	if w.failCreates {
		return nil, status.Error(codes.ResourceExhausted, "test create failure")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.tracker.release:
	}
	start := req.GetRequest()
	return &core.InstanceState{
		InstanceId: start.GetInstanceId(),
		WorkerId:   start.GetWorkerId(),
		Address:    fmt.Sprintf("instance-%d", start.GetInstanceId()),
		Ready:      true,
	}, nil
}

func (w *blockingStartWorker) StopSandbox(ctx context.Context, _ *workerpb.StopSandboxRequest) (*workerpb.StopSandboxResponse, error) {
	if w.stopStarted != nil {
		w.stopStarted <- struct{}{}
	}
	if w.stopRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w.stopRelease:
		}
	}
	return &workerpb.StopSandboxResponse{}, nil
}

type leastLoadedScheduler struct{}

func (leastLoadedScheduler) PickWorker(_ context.Context, _ *core.FunctionSpec, workers []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	var selected *core.WorkerState
	for _, worker := range workers {
		if worker == nil || !worker.GetHealthy() || !worker.GetSchedulable() {
			continue
		}
		if selected == nil || worker.GetInstances() < selected.GetInstances() {
			selected = worker
		}
	}
	if selected == nil {
		return &core.PlacementDecision{Reason: "no worker capacity"}, nil
	}
	return &core.PlacementDecision{WorkerId: selected.GetWorkerId(), Reason: "test"}, nil
}

func newPumpTestActuator(t *testing.T, workerCount int, tokens, global uint32) (*SandboxActuator, *startTracker, context.Context, context.CancelFunc) {
	return newPumpTestActuatorWithCreateFailure(t, workerCount, tokens, global, false)
}

func newPumpTestActuatorWithCreateFailure(t *testing.T, workerCount int, tokens, global uint32, failCreates bool) (*SandboxActuator, *startTracker, context.Context, context.CancelFunc) {
	t.Helper()
	tracker := newStartTracker(workerCount)
	clients := make([]*leafworker.Client, 0, workerCount)
	for i := range workerCount {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		workerpb.RegisterSandboxServiceServer(server, &blockingStartWorker{index: i, tracker: tracker, failCreates: failCreates})
		go func() { _ = server.Serve(listener) }()
		client, err := leafworker.NewClient(context.Background(), i, listener.Addr().String(), time.Second, time.Second, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		clients = append(clients, client)
		t.Cleanup(func() {
			_ = client.Close()
			server.Stop()
			_ = listener.Close()
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	spec := &core.FunctionSpec{
		FunctionId: 1,
		Runtime:    &core.RuntimeSpec{Protocol: "http"},
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	store := dataplane.NewStore()
	pool := testPoolFromStore(store, 1)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: 1,
		Spec:       spec,
		LeafCfg: leaf.LeafConfig{Dataplane: leaf.DataplaneConfig{
			StartTokensPerWorker:  tokens,
			MaxStartsPerReconcile: global,
			StopTimeout:           time.Second,
		}},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:   clients,
		Scheduler: leastLoadedScheduler{},
		Store:     store,
		Pool:      pool,
		Reporter:  discardPublisher{},
	})
	return actuator, tracker, ctx, cancel
}

func applyTarget(t *testing.T, actuator *SandboxActuator, ctx context.Context, desired uint32) {
	t.Helper()
	if err := actuator.ApplyScale(ctx, nil, &core.ScaleDecision{DesiredInstances: uint64(desired), Reason: "cold-start-panic"}); err != nil {
		t.Fatalf("ApplyScale(%d): %v", desired, err)
	}
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}

func TestApplyScaleIsNonBlockingAndPumpRefillsWithinBounds(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 2, 2, 3)
	startedAt := time.Now()
	applyTarget(t, actuator, ctx, 7)
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("ApplyScale blocked for %v", elapsed)
	}
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == 3 }, "initial start batch did not reach global limit")
	_, maxGlobal, maxWorkers := tracker.snapshot()
	if maxGlobal > 3 {
		t.Fatalf("global concurrent starts = %d, want <= 3", maxGlobal)
	}
	for worker, maximum := range maxWorkers {
		if maximum > 2 {
			t.Fatalf("worker %d concurrent starts = %d, want <= 2", worker, maximum)
		}
	}
	for range 7 {
		tracker.release <- struct{}{}
	}
	waitFor(t, func() bool { return actuator.Stats().ActualInstances == 7 }, "pump did not refill to target")
	_, maxGlobal, maxWorkers = tracker.snapshot()
	if maxGlobal > 3 || maxWorkers[0] > 2 || maxWorkers[1] > 2 {
		t.Fatalf("observed concurrency global=%d workers=%v", maxGlobal, maxWorkers)
	}
}

func TestRepeatedTargetsUseOnePumpWithoutOvershoot(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 2, 2, 4)
	for range 20 {
		applyTarget(t, actuator, ctx, 5)
	}
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == 4 }, "pump did not fill start slots")
	applyTarget(t, actuator, ctx, 2)
	for range 4 {
		tracker.release <- struct{}{}
	}
	waitFor(t, func() bool { return actuator.Stats().ActualInstances == 2 }, "reduced target was not reached")
	for range 20 {
		applyTarget(t, actuator, ctx, 5)
	}
	for range 3 {
		tracker.release <- struct{}{}
	}
	waitFor(t, func() bool { return actuator.Stats().ActualInstances == 5 }, "increased target was not reached")
	time.Sleep(20 * time.Millisecond)
	stats := actuator.Stats()
	if stats.ActualInstances != 5 || stats.PendingStarts != 0 {
		t.Fatalf("instances=%d pending=%d, want 5 and 0", stats.ActualInstances, stats.PendingStarts)
	}
	actuator.mu.Lock()
	active := actuator.scaleUpActive
	actuator.mu.Unlock()
	if !active {
		t.Fatal("single pump should remain active awaiting target changes")
	}
}

func TestZeroStartTokensUsesEightPerWorker(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 1, 0, 0)
	applyTarget(t, actuator, ctx, 12)
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == defaultStartBurstPerWorker }, "zero-token default was not applied")
	time.Sleep(20 * time.Millisecond)
	inFlight, maxGlobal, maxWorkers := tracker.snapshot()
	if inFlight != 8 || maxGlobal != 8 || maxWorkers[0] != 8 {
		t.Fatalf("zero-token concurrency current=%d global=%d worker=%d, want 8", inFlight, maxGlobal, maxWorkers[0])
	}
}

func TestDirigentAdmissionDispatchesFullDesiredGap(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 1, 1, 1)
	actuator.cfg.Dataplane.DirigentStrictAdmission = true
	applyTarget(t, actuator, ctx, 12)
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == 12 }, "Dirigent admission did not dispatch the full desired gap")

	_, maxGlobal, maxWorkers := tracker.snapshot()
	if maxGlobal != 12 || maxWorkers[0] != 12 {
		t.Fatalf("concurrency global=%d worker=%d, want 12", maxGlobal, maxWorkers[0])
	}
}

func TestDirigentStrictFailedBatchWaitsForAnotherDecision(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuatorWithCreateFailure(t, 1, 1, 1, true)
	actuator.cfg.Dataplane.DirigentStrictAdmission = true
	applyTarget(t, actuator, ctx, 3)
	waitFor(t, func() bool { return tracker.startCount() == 3 }, "strict batch did not dispatch all starts")
	waitFor(t, func() bool {
		stats := actuator.Stats()
		actuator.mu.Lock()
		active := actuator.strictBatchActive
		actuator.mu.Unlock()
		return !active && stats.PendingStarts == 0
	}, "failed strict batch did not complete")
	time.Sleep(20 * time.Millisecond)
	if got := tracker.startCount(); got != 3 {
		t.Fatalf("failed strict batch retried %d creates, want 3", got)
	}

	// The next controller decision, even with the same target, creates the next batch.
	applyTarget(t, actuator, ctx, 3)
	waitFor(t, func() bool { return tracker.startCount() == 6 }, "later strict decision did not create a new batch")
}

func TestDirigentStrictApplyScaleHandoffsDecisionsWithoutCoalescing(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 1, 1, 1)
	actuator.cfg.Dataplane.DirigentStrictAdmission = true
	applyTarget(t, actuator, ctx, 3)
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == 3 }, "first strict batch did not start")

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- actuator.ApplyScale(ctx, nil, &core.ScaleDecision{DesiredInstances: 4, Reason: "next-decision"})
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second strict ApplyScale returned before the first batch completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	for range 3 {
		tracker.release <- struct{}{}
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second strict ApplyScale: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second strict ApplyScale remained blocked after the first batch")
	}
	waitFor(t, func() bool { return tracker.startCount() == 4 }, "second strict decision did not start its own batch")
	if got := tracker.startCount(); got != 4 {
		t.Fatalf("strict starts=%d, want 4 from batches 3 then 1", got)
	}
	tracker.release <- struct{}{}
	waitFor(t, func() bool { return actuator.Stats().ActualInstances == 4 }, "second strict batch did not complete")
}

type placementBarrierScheduler struct {
	calls         atomic.Int32
	secondEntered chan struct{}
	releaseSecond chan struct{}
}

func (s *placementBarrierScheduler) PickWorker(_ context.Context, _ *core.FunctionSpec, _ []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	if s.calls.Add(1) == 2 {
		close(s.secondEntered)
		<-s.releaseSecond
	}
	return &core.PlacementDecision{WorkerId: 1, Reason: "test"}, nil
}

func TestDirigentStrictStartsCreateBeforeAllPlacementsComplete(t *testing.T) {
	actuator, tracker, ctx, _ := newPumpTestActuator(t, 1, 1, 1)
	actuator.cfg.Dataplane.DirigentStrictAdmission = true
	scheduler := &placementBarrierScheduler{
		secondEntered: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	actuator.scheduler = scheduler

	applyTarget(t, actuator, ctx, 2)
	select {
	case <-scheduler.secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second placement did not begin")
	}
	select {
	case <-tracker.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSandbox did not begin while the second placement was blocked")
	}
	close(scheduler.releaseSecond)
	waitFor(t, func() bool { return tracker.startCount() == 2 }, "second CreateSandbox did not begin")
	for range 2 {
		tracker.release <- struct{}{}
	}
	waitFor(t, func() bool { return actuator.Stats().ActualInstances == 2 }, "strict placement batch did not complete")
}

func TestDirigentStrictDownscaleForNewServiceStartsTeardownAndDoesNotBlockNextDecision(t *testing.T) {
	tracker := newStartTracker(1)
	stopStarted := make(chan struct{}, 10)
	stopRelease := make(chan struct{})
	workerService := &blockingStartWorker{
		tracker: tracker, stopStarted: stopStarted, stopRelease: stopRelease,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	workerpb.RegisterSandboxServiceServer(server, workerService)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		close(stopRelease)
		server.Stop()
		_ = listener.Close()
	})
	workerClient, err := leafworker.NewClient(context.Background(), 0, listener.Addr().String(), time.Second, time.Second, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = workerClient.Close() })

	const functionID = 1
	const initialInstances = 10
	spec := &core.FunctionSpec{FunctionId: functionID, Runtime: &core.RuntimeSpec{Protocol: "http"}, Scale: &core.ScalePolicySpec{MaxConcurrency: 1, MaxQueueDepth: 1}}
	store := dataplane.NewStore()
	for i := range initialInstances {
		instanceID := uint64(i + 1)
		address := fmt.Sprintf("instance-%d", instanceID)
		if err := store.PutInstance(context.Background(), &core.InstanceState{FunctionId: functionID, InstanceId: instanceID, Address: address, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	pool := testPoolFromStore(store, functionID)
	actuator := NewSandboxActuator(ActuatorConfig{
		FunctionID: functionID,
		Spec:       spec,
		LeafCfg:    leaf.LeafConfig{Dataplane: leaf.DataplaneConfig{DirigentStrictAdmission: true, StopTimeout: time.Second}},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workers:    []*leafworker.Client{workerClient},
		Scheduler:  leastLoadedScheduler{},
		Store:      store,
		Pool:       pool,
		Reporter:   discardPublisher{},
	})
	for i := range initialInstances {
		instanceID := uint64(i + 1)
		actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{id: instanceID, address: fmt.Sprintf("instance-%d", instanceID)})
	}
	actuator.totalInstances = initialInstances
	actuator.strictOptimisticScale = initialInstances

	downCtx, cancelDown := context.WithCancel(context.Background())
	if err := actuator.ApplyScale(downCtx, nil, &core.ScaleDecision{DesiredInstances: 2, Reason: "test-down"}); err != nil {
		t.Fatalf("downscale ApplyScale: %v", err)
	}
	select {
	case <-stopStarted:
	case <-time.After(time.Second):
		t.Fatal("strict downscale did not start teardown")
	}
	cancelDown()

	logicalScale, ok := actuator.LogicalScale()
	if !ok || logicalScale != 2 {
		t.Fatalf("logical scale = %d, known=%t, want 2 and true", logicalScale, ok)
	}
	now := time.Now()
	logicalPolicy := &DefaultPolicy{ScaleToZeroAfter: time.Minute, DirigentAdmission: true}
	logicalDecision, err := logicalPolicy.DesiredScale(context.Background(), spec, Signals{
		ReadyInstances: initialInstances, LogicalScale: logicalScale, HasLogicalScale: true, InFlight: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	stalePolicy := &DefaultPolicy{ScaleToZeroAfter: time.Minute, DirigentAdmission: true}
	staleDecision, err := stalePolicy.DesiredScale(context.Background(), spec, Signals{ReadyInstances: initialInstances, InFlight: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if logicalDecision.GetDesiredInstances() != 1 || staleDecision.GetDesiredInstances() != 5 {
		t.Fatalf("logical/stale desired = %d/%d, want 1/5", logicalDecision.GetDesiredInstances(), staleDecision.GetDesiredInstances())
	}

	upDone := make(chan error, 1)
	go func() {
		upDone <- actuator.ApplyScale(context.Background(), nil, &core.ScaleDecision{DesiredInstances: 3, Reason: "test-up"})
	}()
	select {
	case err := <-upDone:
		if err != nil {
			t.Fatalf("scale-up ApplyScale: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("slow strict teardown blocked the next strict decision handoff")
	}
	select {
	case <-tracker.started:
	case <-time.After(time.Second):
		t.Fatal("next strict scale-up did not dispatch while teardown was slow")
	}
	logicalScale, ok = actuator.LogicalScale()
	if !ok || logicalScale != 3 {
		t.Fatalf("logical scale after scale-up = %d, known=%t, want 3 and true", logicalScale, ok)
	}
	pending := actuator.Stats().PendingStarts
	decisionAfterUp, err := (&DefaultPolicy{ScaleToZeroAfter: time.Minute, DirigentAdmission: true}).DesiredScale(context.Background(), spec, Signals{
		ReadyInstances: initialInstances, PendingStarts: uint32(pending), LogicalScale: logicalScale, HasLogicalScale: true, InFlight: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decisionAfterUp.GetDesiredInstances() != 1 {
		t.Fatalf("desired after scale-up = %d, want 1 from logical scale without pending double-count", decisionAfterUp.GetDesiredInstances())
	}
}

func TestScaleUpPumpStopsOnContextCancellation(t *testing.T) {
	actuator, tracker, ctx, cancel := newPumpTestActuator(t, 1, 2, 0)
	applyTarget(t, actuator, ctx, 4)
	waitFor(t, func() bool { inFlight, _, _ := tracker.snapshot(); return inFlight == 2 }, "starts did not enter worker")
	cancel()
	waitFor(t, func() bool {
		stats := actuator.Stats()
		actuator.mu.Lock()
		active := actuator.scaleUpActive
		actuator.mu.Unlock()
		return !active && stats.PendingStarts == 0
	}, "pump or starts remained after cancellation")
}
