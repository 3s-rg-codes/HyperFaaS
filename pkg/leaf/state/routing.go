package state

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

// RoutingProjection selects which routing-state fields a subscriber needs.
//
// The reporter calls only the source methods selected here, so a field left
// false is never computed, allocated, or sent. This is the producer half of
// state gating: a subscriber that runs, for example, random routing requests no
// leaf load and no per-function capacity, and the leaf never builds them.
type RoutingProjection struct {
	LeafLoad          bool
	AggregateInFlight bool
	FunctionCapacity  bool
}

// RoutingFrame is one leaf's routing state for one configuration version.
//
// The first frame of a stream is a full snapshot for the requested projection.
// Later frames carry only capacity changes; scalar fields are included whenever
// requested so they do not go stale between capacity changes. Capacities use a
// Deleted tombstone for functions that disappeared.
type RoutingFrame struct {
	ConfigVersion uint64
	LeafID        uint64
	// Revision is monotonic within one stream. Consumers reject a frame whose
	// revision is not newer than the last one they applied.
	Revision     uint64
	FullSnapshot bool
	// HealthyWorkers is the number of workers that can accept new sandboxes. A
	// connected leaf with zero healthy workers must not receive routing
	// decisions.
	HealthyWorkers uint32
	// LeafLoad is set only when the projection requests it.
	LeafLoad    float64
	HasLeafLoad bool
	// AggregateInFlight is set only when the projection requests it.
	AggregateInFlight    uint64
	HasAggregateInFlight bool
	// Capacities is nil unless the projection requests function capacity.
	Capacities []*core.FunctionCapacity
}

// routingSubscriber is one active WatchRoutingState stream.
type routingSubscriber struct {
	projection    RoutingProjection
	configVersion uint64
	notify        chan struct{}

	// sent is the last capacity view delivered to this subscriber. Deltas are
	// computed against it, so coalescing several source changes into one frame
	// never loses an update: the frame always brings the subscriber from its
	// own last view to the current one.
	sent map[uint64]*core.FunctionCapacity
}

// WatchRoutingState streams projection-gated routing frames until ctx is done.
//
// The returned channels are closed when the stream ends. The caller must read
// the frame channel; the reporter never drops a frame, so a slow reader applies
// backpressure to its own stream only.
func (r *Reporter) WatchRoutingState(ctx context.Context, configVersion uint64, projection RoutingProjection) (<-chan *RoutingFrame, <-chan error) {
	sub := &routingSubscriber{
		projection:    projection,
		configVersion: configVersion,
		notify:        make(chan struct{}, 1),
	}
	if projection.FunctionCapacity {
		sub.sent = make(map[uint64]*core.FunctionCapacity)
	}

	r.mu.Lock()
	r.nextRoutingSubID++
	id := r.nextRoutingSubID
	if r.routingSubs == nil {
		r.routingSubs = make(map[uint64]*routingSubscriber)
	}
	r.routingSubs[id] = sub
	r.mu.Unlock()

	frames := make(chan *RoutingFrame, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(frames)
		defer close(errs)
		defer func() {
			r.mu.Lock()
			delete(r.routingSubs, id)
			r.mu.Unlock()
		}()

		// The first frame is always a full snapshot for the projection, so the
		// consumer needs no separate snapshot request.
		if err := r.emit(ctx, sub, frames, true); err != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.notify:
				if err := r.emit(ctx, sub, frames, false); err != nil {
					return
				}
			}
		}
	}()
	return frames, errs
}

// emit builds and sends one frame. It must run on the subscriber goroutine.
func (r *Reporter) emit(ctx context.Context, sub *routingSubscriber, out chan<- *RoutingFrame, full bool) error {
	frame := &RoutingFrame{
		ConfigVersion:  sub.configVersion,
		LeafID:         r.leafID,
		Revision:       r.revision.Add(1),
		FullSnapshot:   full,
		HealthyWorkers: r.source.HealthyWorkers(),
	}
	if sub.projection.LeafLoad {
		frame.LeafLoad = r.source.LeafLoad()
		frame.HasLeafLoad = true
	}
	if sub.projection.AggregateInFlight {
		frame.AggregateInFlight = r.source.AggregateInFlight()
		frame.HasAggregateInFlight = true
	}
	if sub.projection.FunctionCapacity {
		current := r.source.CapacitySnapshot()
		if full {
			sub.sent = make(map[uint64]*core.FunctionCapacity, len(current))
			for id, cap := range current {
				sub.sent[id] = cap
				frame.Capacities = append(frame.Capacities, cap)
			}
		} else {
			frame.Capacities = diffCapacities(sub.sent, current)
			sub.sent = current
		}
	}

	select {
	case out <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// diffCapacities returns the entries that differ between sent and current, plus
// tombstones for functions present in sent but missing from current.
func diffCapacities(sent, current map[uint64]*core.FunctionCapacity) []*core.FunctionCapacity {
	var out []*core.FunctionCapacity
	for id, cap := range current {
		if prev, ok := sent[id]; !ok || !capacityEqual(prev, cap) {
			out = append(out, cap)
		}
	}
	for id, prev := range sent {
		if _, ok := current[id]; ok {
			continue
		}
		if prev.GetDeleted() {
			continue
		}
		out = append(out, &core.FunctionCapacity{FunctionId: id, Deleted: true})
	}
	return out
}

// notifyRoutingLocked wakes every routing subscriber. The caller must hold
// r.mu. Waking is coalescing: a subscriber that is already pending is not
// signalled twice, because when it wakes it computes a delta from its own last
// delivered view.
func (r *Reporter) notifyRoutingLocked() {
	for _, sub := range r.routingSubs {
		select {
		case sub.notify <- struct{}{}:
		default:
		}
	}
}
