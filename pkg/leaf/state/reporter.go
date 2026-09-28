package state

import (
	"context"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
)

type capacitySource interface {
	CapacitySnapshot() map[uint64]*core.FunctionCapacity
	WorkerHealthy() bool
	LeafLoad() float64
	// HealthyWorkers is the number of workers that can accept new sandboxes.
	// It is the health signal for routing; stream liveness alone is not enough
	// because a connected leaf can have no usable workers.
	HealthyWorkers() uint32
	// AggregateInFlight is the total in-flight invocation count across all
	// functions. The least-loaded routing policy consumes this single scalar.
	AggregateInFlight() uint64
}

// Reporter publishes routing state to projection-gated subscribers.
//
// The routing request path uses WatchRoutingState. CurrentState remains as a
// diagnostic full snapshot for tooling and tests; it is not on the routing
// path.
type Reporter struct {
	leafID uint64
	source capacitySource
	mu     sync.Mutex
	// last records the most recent capacity of every function so CurrentState
	// can include deletion tombstones.
	last map[uint64]*core.FunctionCapacity

	// routingSubs are the projection-gated WatchRoutingState streams.
	routingSubs      map[uint64]*routingSubscriber
	nextRoutingSubID uint64
	// revision is a leaf-global monotonic counter. It never resets, so a
	// reconnecting consumer can keep rejecting stale frames across sessions.
	revision atomic.Uint64
}

func NewReporter(leafID uint64, source capacitySource) *Reporter {
	return &Reporter{
		leafID: leafID,
		source: source,
		last:   make(map[uint64]*core.FunctionCapacity),
	}
}

// CurrentState builds a diagnostic full snapshot. It computes every field, so
// it must not be used on the routing request path.
func (r *Reporter) CurrentState(_ context.Context) (*core.LeafState, error) {
	now := timestamppb.Now()
	snap := r.source.CapacitySnapshot()
	r.mu.Lock()
	for functionID, cap := range r.last {
		if cap.GetDeleted() {
			snap[functionID] = cap
		}
	}
	r.mu.Unlock()
	functions := make([]*core.FunctionCapacity, 0, len(snap))
	for _, cap := range snap {
		functions = append(functions, cap)
	}
	return &core.LeafState{
		LeafId:     r.leafID,
		Healthy:    r.source.WorkerHealthy(),
		Functions:  functions,
		ObservedAt: now,
		UpdateTime: now,
		LeafLoad:   r.source.LeafLoad(),
	}, nil
}

// Publish notifies subscribers that one function's capacity changed. The change
// is not materialized here: each subscriber diffs against its own last-delivered
// view when it emits.
func (r *Reporter) Publish(cap *core.FunctionCapacity) {
	if cap == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.last[cap.GetFunctionId()]
	if prev != nil && capacityEqual(prev, cap) {
		return
	}
	r.last[cap.GetFunctionId()] = cap
	r.notifyRoutingLocked()
}

// PublishHeartbeat wakes subscribers even when no capacity changed. This is
// what keeps scalar signals (leaf load, aggregate in-flight) fresh between
// capacity changes.
//
// It deliberately does not compute the capacity snapshot: only a subscriber
// whose projection requests capacity builds it, and only when it emits.
func (r *Reporter) PublishHeartbeat() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifyRoutingLocked()
}

// PublishDeleted notifies subscribers that a function was removed.
func (r *Reporter) PublishDeleted(functionID uint64) {
	cap := &core.FunctionCapacity{
		FunctionId: functionID,
		Deleted:    true,
		Status:     core.CapacityStatus_CAPACITY_STATUS_UNKNOWN,
		ObservedAt: timestamppb.Now(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last[functionID] = cap
	r.notifyRoutingLocked()
}

func capacityEqual(a, b *core.FunctionCapacity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.GetReadyInstances() == b.GetReadyInstances() &&
		a.GetAvailableConcurrency() == b.GetAvailableConcurrency() &&
		a.GetInFlight() == b.GetInFlight() &&
		a.GetDeleted() == b.GetDeleted() &&
		a.GetStatus() == b.GetStatus()
}
