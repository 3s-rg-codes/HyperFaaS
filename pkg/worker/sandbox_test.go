package worker

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	workerruntime "hyperfaas-ideal-arch/pkg/worker/runtime"
)

type lifecycleRuntime struct {
	address string
	events  chan workerruntime.LifecycleEvent
	watched chan uint64
}

func (r *lifecycleRuntime) Prepare(context.Context, *core.FunctionSpec) (*core.PreparedArtifact, error) {
	return nil, nil
}

func (r *lifecycleRuntime) HasImage(context.Context, string) (bool, error) { return true, nil }

func (r *lifecycleRuntime) Start(context.Context, *core.StartSandboxRequest) (*core.InstanceState, error) {
	return &core.InstanceState{InstanceId: 42, Address: r.address, Ready: true}, nil
}

func (r *lifecycleRuntime) Stop(context.Context, uint64) error { return nil }

func (r *lifecycleRuntime) Stats(context.Context, uint64) (*core.ResourceUsage, error) {
	return &core.ResourceUsage{}, nil
}

func (r *lifecycleRuntime) WatchLifecycle(_ context.Context, instanceID uint64) (<-chan workerruntime.LifecycleEvent, error) {
	r.watched <- instanceID
	return r.events, nil
}

func TestNormalRemovalDoesNotEmitEventButLifecycleExitDoes(t *testing.T) {
	sandbox := &Sandbox{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		readySignals: NewReadySignals(),
		instances:    make(map[uint64]*sandboxRecord),
		removalsSubs: make(map[int]chan SandboxRemoval),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	removals := sandbox.SubscribeSandboxRemovals(ctx)

	sandbox.mu.Lock()
	sandbox.stateRevision = 1
	sandbox.instances[7] = &sandboxRecord{state: &core.InstanceState{InstanceId: 7, FunctionId: 3}}
	sandbox.mu.Unlock()

	// Leaf-initiated stop path: no event.
	sandbox.removeInstance(7)
	select {
	case event := <-removals:
		t.Fatalf("normal removal emitted unexpected event: %+v", event)
	default:
	}

	// Abnormal exit path: event with the post-removal revision.
	sandbox.mu.Lock()
	sandbox.instances[8] = &sandboxRecord{state: &core.InstanceState{InstanceId: 8, FunctionId: 3}}
	sandbox.mu.Unlock()
	sandbox.removeInstanceAndNotify(8)
	select {
	case event := <-removals:
		if event.InstanceID != 8 || event.FunctionID != 3 || event.Revision != 3 {
			t.Fatalf("lifecycle event = %+v, want instance 8 function 3 revision 3", event)
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle exit did not emit a removal event")
	}
}

func TestReadySandboxWatchesLifecycleAndRemovesExitedInstance(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	rt := &lifecycleRuntime{
		address: listener.Addr().String(),
		events:  make(chan workerruntime.LifecycleEvent, 1),
		watched: make(chan uint64, 1),
	}
	sandbox := &Sandbox{
		runtime:      rt,
		readySignals: NewReadySignals(),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		startTimeout: time.Second,
		instances:    make(map[uint64]*sandboxRecord),
		removalsSubs: make(map[int]chan SandboxRemoval),
	}
	subCtx, cancelSub := context.WithCancel(context.Background())
	defer cancelSub()
	removals := sandbox.SubscribeSandboxRemovals(subCtx)

	state, err := sandbox.CreateSandbox(context.Background(), &core.StartSandboxRequest{InstanceId: 42})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if state.GetInstanceId() != 42 {
		t.Fatalf("instance ID = %d, want 42", state.GetInstanceId())
	}
	if state.GetWorkerStateRevision() != 1 {
		t.Fatalf("create revision = %d, want 1", state.GetWorkerStateRevision())
	}
	instances, revision := sandbox.SandboxStateSnapshot()
	if revision != 1 || len(instances) != 1 || instances[0].GetInstanceId() != 42 {
		t.Fatalf("created snapshot revision/instances = %d/%v, want revision 1 with instance 42", revision, instances)
	}
	// Creation is not a removal; no event must be emitted.
	select {
	case event := <-removals:
		t.Fatalf("creation emitted unexpected removal event: %+v", event)
	default:
	}

	select {
	case instanceID := <-rt.watched:
		if instanceID != 42 {
			t.Fatalf("watched instance ID = %d, want 42", instanceID)
		}
	case <-time.After(time.Second):
		t.Fatal("ready sandbox lifecycle was not watched")
	}

	rt.events <- workerruntime.LifecycleExit
	select {
	case event := <-removals:
		if event.InstanceID != 42 || event.Revision != 2 {
			t.Fatalf("removal event = %+v, want instance 42 revision 2", event)
		}
	case <-time.After(time.Second):
		t.Fatal("sandbox lifecycle exit did not publish a removal event")
	}
	instances, revision = sandbox.SandboxStateSnapshot()
	if revision != 2 || len(instances) != 0 {
		t.Fatalf("exit snapshot revision/count = %d/%d, want 2/0", revision, len(instances))
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		instances, err := sandbox.ListSandboxes(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(instances) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("exited ready sandbox remained in worker state")
}
