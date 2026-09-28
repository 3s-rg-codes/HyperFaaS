package state

import (
	"context"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

type fakeSource struct {
	mu                sync.Mutex
	load              float64
	healthyWorkers    uint32
	aggregateInFlight uint64
	capacity          map[uint64]*core.FunctionCapacity
	capacityCalls     int
}

func (f *fakeSource) CapacitySnapshot() map[uint64]*core.FunctionCapacity {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capacityCalls++
	out := make(map[uint64]*core.FunctionCapacity, len(f.capacity))
	for id, cap := range f.capacity {
		out[id] = cap
	}
	return out
}

func (f *fakeSource) WorkerHealthy() bool { return true }

func (f *fakeSource) LeafLoad() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load
}

func (f *fakeSource) HealthyWorkers() uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthyWorkers
}

func (f *fakeSource) AggregateInFlight() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.aggregateInFlight
}

func (f *fakeSource) capacityCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capacityCalls
}

func (f *fakeSource) setCapacity(id uint64, cap *core.FunctionCapacity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capacity[id] = cap
}

func TestHeartbeatFrameCarriesLeafLoad(t *testing.T) {
	src := &fakeSource{load: 2.25}
	r := NewReporter(7, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames, _ := r.WatchRoutingState(ctx, 1, RoutingProjection{LeafLoad: true})
	frame := nextFrame(t, frames)
	if frame.LeafID != 7 {
		t.Fatalf("leaf_id=%d", frame.LeafID)
	}
	if !frame.HasLeafLoad || frame.LeafLoad != 2.25 {
		t.Fatalf("leaf_load=%v has=%v, want 2.25", frame.LeafLoad, frame.HasLeafLoad)
	}
	if frame.Capacities != nil {
		t.Fatalf("leaf-load-only projection must not include capacities, got %d", len(frame.Capacities))
	}

	state, err := r.CurrentState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.GetLeafLoad() != 2.25 {
		t.Fatalf("snapshot leaf_load=%v", state.GetLeafLoad())
	}
}

// TestWatchRoutingStateGatesCapacityProduction is the producer-side gating
// guarantee: a projection that needs no per-function capacity must never call
// CapacitySnapshot, so no FunctionCapacity message is ever allocated.
func TestWatchRoutingStateGatesCapacityProduction(t *testing.T) {
	src := &fakeSource{healthyWorkers: 3}
	r := NewReporter(7, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames, _ := r.WatchRoutingState(ctx, 4, RoutingProjection{})
	frame := nextFrame(t, frames)
	if !frame.FullSnapshot {
		t.Fatal("first frame must be a full snapshot")
	}
	if frame.ConfigVersion != 4 || frame.LeafID != 7 || frame.HealthyWorkers != 3 {
		t.Fatalf("frame header = %+v", frame)
	}
	if frame.HasLeafLoad || frame.HasAggregateInFlight || frame.Capacities != nil {
		t.Fatalf("unrequested fields were produced: %+v", frame)
	}
	if got := src.capacityCallsCount(); got != 0 {
		t.Fatalf("CapacitySnapshot called %d times for a projection that does not need it", got)
	}
}

func TestWatchRoutingStateSendsFullSnapshotThenDelta(t *testing.T) {
	src := &fakeSource{
		load:              0.5,
		healthyWorkers:    2,
		aggregateInFlight: 7,
		capacity: map[uint64]*core.FunctionCapacity{
			1: {FunctionId: 1, InFlight: 1},
		},
	}
	r := NewReporter(7, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames, _ := r.WatchRoutingState(ctx, 9, RoutingProjection{
		LeafLoad:          true,
		AggregateInFlight: true,
		FunctionCapacity:  true,
	})
	first := nextFrame(t, frames)
	if !first.FullSnapshot || first.ConfigVersion != 9 || first.HealthyWorkers != 2 {
		t.Fatalf("first frame = %+v", first)
	}
	if !first.HasLeafLoad || first.LeafLoad != 0.5 {
		t.Fatalf("leaf load = %+v", first)
	}
	if !first.HasAggregateInFlight || first.AggregateInFlight != 7 {
		t.Fatalf("aggregate in-flight = %+v", first)
	}
	if len(first.Capacities) != 1 {
		t.Fatalf("full snapshot capacities = %d, want 1", len(first.Capacities))
	}

	src.setCapacity(2, &core.FunctionCapacity{FunctionId: 2, InFlight: 3})
	r.Publish(&core.FunctionCapacity{FunctionId: 2, InFlight: 3})

	second := nextFrame(t, frames)
	if second.FullSnapshot {
		t.Fatal("a follow-up frame must be a delta")
	}
	if len(second.Capacities) != 1 || second.Capacities[0].GetFunctionId() != 2 {
		t.Fatalf("delta capacities = %+v, want only function 2", second.Capacities)
	}
	if second.Revision <= first.Revision {
		t.Fatalf("revision did not advance: %d then %d", first.Revision, second.Revision)
	}
}

func nextFrame(t *testing.T, frames <-chan *RoutingFrame) *RoutingFrame {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routing frame")
	}
	return nil
}
