package worker

import (
	"context"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

type stateChangeSandbox struct {
	mu        sync.Mutex
	instances []*core.InstanceState
	revision  uint64
	listCalls int
}

func (s *stateChangeSandbox) PrepareImage(context.Context, *core.FunctionSpec) (*core.PreparedArtifact, error) {
	return nil, nil
}
func (s *stateChangeSandbox) HasImage(context.Context, string) (bool, error) { return true, nil }
func (s *stateChangeSandbox) CreateSandbox(context.Context, *core.StartSandboxRequest) (*core.InstanceState, error) {
	return nil, nil
}
func (s *stateChangeSandbox) StopSandbox(context.Context, uint64) error { return nil }
func (s *stateChangeSandbox) ListSandboxes(context.Context) ([]*core.InstanceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	return append([]*core.InstanceState(nil), s.instances...), nil
}
func (s *stateChangeSandbox) SignalReady(context.Context, uint64) error { return nil }
func (s *stateChangeSandbox) CachedImages() []*core.CachedImage         { return nil }
func (s *stateChangeSandbox) SandboxStateSnapshot() ([]*core.InstanceState, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*core.InstanceState(nil), s.instances...), s.revision
}

func TestLoadAverageNormPresentAndNormalized(t *testing.T) {
	health := NewProcHealthService(WorkerConfig{}, nil)
	state, err := health.CurrentState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := state.GetLoadAverageNorm()
	if got < 0 {
		t.Fatalf("load_average_norm=%v, want >= 0", got)
	}
	want, err := expectedLoadAverageNorm()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-want) > 0.05 {
		t.Fatalf("load_average_norm=%v, want ~%v", got, want)
	}
}

func TestLoadAverageNormOverride(t *testing.T) {
	health := NewProcHealthService(WorkerConfig{}, nil)
	health.SetLoadAverageNormOverride(2.5, false)
	state, err := health.CurrentState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GetLoadAverageNorm() != 2.5 {
		t.Fatalf("override=%v, want 2.5", state.GetLoadAverageNorm())
	}
	health.SetLoadAverageNormOverride(0, true)
	state, err = health.CurrentState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GetLoadAverageNorm() == 2.5 {
		t.Fatal("clear did not restore sampled load")
	}
}

func TestWatchStatePublishesPeriodicSnapshotWithoutSlowList(t *testing.T) {
	sandbox := &stateChangeSandbox{}
	health := NewProcHealthService(WorkerConfig{Stats: StatsConfig{MetricsInterval: 20 * time.Millisecond}}, sandbox)
	health.SetLoadAverageNormOverride(0, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, errs := health.WatchState(ctx, FullStateProjection())

	select {
	case state := <-updates:
		if len(state.GetSandboxStates()) != 0 {
			t.Fatalf("initial sandbox count = %d, want 0", len(state.GetSandboxStates()))
		}
	case err := <-errs:
		t.Fatalf("initial state error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("initial worker state was not published")
	}

	sandbox.mu.Lock()
	sandbox.instances = []*core.InstanceState{{FunctionId: 1, InstanceId: 11, Ready: true}}
	sandbox.revision = 4
	sandbox.mu.Unlock()

	// WatchState is a periodic relist: the change is picked up on the next
	// metrics tick, not pushed synchronously.
	deadline := time.After(time.Second)
	for {
		select {
		case state := <-updates:
			if state.GetSandboxRevision() != 4 {
				continue
			}
			if len(state.GetSandboxStates()) != 1 || state.GetSandboxStates()[0].GetInstanceId() != 11 {
				t.Fatalf("lifecycle state = %+v, want instance 11", state.GetSandboxStates())
			}
			sandbox.mu.Lock()
			defer sandbox.mu.Unlock()
			if sandbox.listCalls != 0 {
				t.Fatalf("slow ListSandboxes calls = %d, want 0 for worker state snapshots", sandbox.listCalls)
			}
			return
		case err := <-errs:
			t.Fatalf("lifecycle state error: %v", err)
		case <-deadline:
			t.Fatal("periodic snapshot did not reflect sandbox lifecycle change")
		}
	}
}

func TestWatchStateGatesUnrequestedSignalsAtSource(t *testing.T) {
	sandbox := &countingCacheSandbox{}
	health := NewProcHealthService(WorkerConfig{Stats: StatsConfig{MetricsInterval: time.Hour}}, sandbox)
	health.SetLoadAverageNormOverride(2.5, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, errs := health.WatchState(ctx, StateProjection{})
	select {
	case state := <-updates:
		if state.GetLoadAverageNorm() != 0 {
			t.Fatalf("load_average_norm=%v, want 0 when not requested", state.GetLoadAverageNorm())
		}
		if len(state.GetCachedImages()) != 0 {
			t.Fatalf("cached_images=%v, want none when not requested", state.GetCachedImages())
		}
	case err := <-errs:
		t.Fatalf("state error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("worker state was not published")
	}
	if sandbox.cachedCalls != 0 {
		t.Fatalf("CachedImages calls=%d, want 0 when cached_images is not requested", sandbox.cachedCalls)
	}
}

func TestWatchStateComputesRequestedSignals(t *testing.T) {
	sandbox := &countingCacheSandbox{}
	health := NewProcHealthService(WorkerConfig{Stats: StatsConfig{MetricsInterval: time.Hour}}, sandbox)
	health.SetLoadAverageNormOverride(2.5, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, errs := health.WatchState(ctx, StateProjection{LoadAverageNorm: true, CachedImages: true})
	select {
	case state := <-updates:
		if state.GetLoadAverageNorm() != 2.5 {
			t.Fatalf("load_average_norm=%v, want 2.5", state.GetLoadAverageNorm())
		}
		if len(state.GetCachedImages()) != 1 {
			t.Fatalf("cached_images=%v, want one entry", state.GetCachedImages())
		}
	case err := <-errs:
		t.Fatalf("state error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("worker state was not published")
	}
	if sandbox.cachedCalls == 0 {
		t.Fatal("CachedImages was never called for a requesting projection")
	}
}

// countingCacheSandbox counts CachedImages calls so the gating test can prove
// the field is not computed when it is not requested.
type countingCacheSandbox struct {
	stateChangeSandbox
	cachedCalls int
}

func (s *countingCacheSandbox) CachedImages() []*core.CachedImage {
	s.cachedCalls++
	return []*core.CachedImage{{Image: "gated-image"}}
}

func expectedLoadAverageNorm() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	n := runtime.NumCPU()
	if n <= 0 {
		n = 1
	}
	return load1 / float64(n), nil
}
