package net

import (
	"context"
	"log/slog"
	"sort"
	"sync"
)

// Throttler manages concurrency limits and load balancing for function instances.
// It handles requests to functions and ensures they don't exceed container concurrency limits.
// In Knative, there is only one general Throttler it has a map to revisionThrottlers
// that handle throttling per revision. Revision is a function id with a version.
// In our case, it just makes sense to have a single Throttler for a function, we don't have versioning.
type Throttler struct {
	// containerConcurrency is the max concurrent requests per instance (0 = unlimited)
	containerConcurrency int

	// breaker is the overall breaker for all instances of this function
	breaker breaker

	// lbPolicy selects which instance to use for a request
	lbPolicy lbPolicy

	// instanceTrackers tracks all available instances
	instanceTrackers []*instanceTracker

	// mux guards the instanceTrackers slice during updates
	mux sync.RWMutex

	logger *slog.Logger
}

// NewThrottler creates a new Throttler for a function with the given container concurrency.
func NewThrottler(containerConcurrency int, logger *slog.Logger) *Throttler {
	var (
		revBreaker breaker
		lbp        lbPolicy
	)

	switch {
	case containerConcurrency == 0:
		revBreaker = newInfiniteBreaker(logger)
		lbp = randomChoice2Policy
	case containerConcurrency <= 3:
		// For very low CC values, use first available instance
		revBreaker = NewBreaker(BreakerParams{
			QueueDepth:      defaultQueueDepth,
			MaxConcurrency:  maxBreakerCapacity,
			InitialCapacity: 0,
		})
		lbp = firstAvailableLBPolicy
	default:
		// Otherwise use round-robin
		revBreaker = NewBreaker(BreakerParams{
			QueueDepth:      defaultQueueDepth,
			MaxConcurrency:  maxBreakerCapacity,
			InitialCapacity: 0,
		})
		lbp = roundRobinLBPolicy()
	}

	return &Throttler{
		containerConcurrency: containerConcurrency,
		breaker:              revBreaker,
		lbPolicy:             lbp,
		instanceTrackers:     make([]*instanceTracker, 0),
		logger:               logger.With("component", "throttler"),
	}
}

// UpdateInstances updates the list of available instances for this function.
// This should be called whenever the set of instances changes (e.g., scaling up/down).
func (t *Throttler) UpdateInstances(addresses []string) {
	t.mux.Lock()
	defer t.mux.Unlock()

	// Create a map for fast lookup of existing trackers
	trackersMap := make(map[string]*instanceTracker, len(t.instanceTrackers))
	for _, tracker := range t.instanceTrackers {
		trackersMap[tracker.address] = tracker
	}

	// Build new trackers list, reusing existing trackers where possible
	trackers := make([]*instanceTracker, 0, len(addresses))
	for _, address := range addresses {
		tracker, ok := trackersMap[address]
		if !ok {
			// Create new tracker
			var b breaker
			if t.containerConcurrency == 0 {
				b = nil // No breaker needed for infinite CC
			} else {
				b = NewBreaker(BreakerParams{
					QueueDepth:      defaultQueueDepth,
					MaxConcurrency:  t.containerConcurrency,
					InitialCapacity: t.containerConcurrency,
				})
			}
			tracker = newInstanceTracker(address, b)
		}
		trackers = append(trackers, tracker)
	}

	// Sort for stable ordering
	sort.Slice(trackers, func(i, j int) bool {
		return trackers[i].address < trackers[j].address
	})

	t.instanceTrackers = trackers
	t.updateCapacity(len(addresses))

	if t.logger != nil {
		t.logger.Debug("Updated instances", "count", len(addresses), "capacity", t.breaker.Capacity())
	}
}

// AddInstance adds a new instance to the throttler.
// If the instance already exists, this is a no-op.
func (t *Throttler) AddInstance(address string) {
	t.mux.Lock()
	defer t.mux.Unlock()

	// Check if address already exists
	for _, tracker := range t.instanceTrackers {
		if tracker.address == address {
			// Instance already exists, no-op
			if t.logger != nil {
				t.logger.Debug("Instance already exists", "address", address)
			}
			return
		}
	}

	// Create new tracker
	var b breaker
	if t.containerConcurrency == 0 {
		b = nil // No breaker needed for infinite CC
	} else {
		b = NewBreaker(BreakerParams{
			QueueDepth:      defaultQueueDepth,
			MaxConcurrency:  t.containerConcurrency,
			InitialCapacity: t.containerConcurrency,
		})
	}
	tracker := newInstanceTracker(address, b)

	// Add to trackers list
	t.instanceTrackers = append(t.instanceTrackers, tracker)

	// Sort for stable ordering
	sort.Slice(t.instanceTrackers, func(i, j int) bool {
		return t.instanceTrackers[i].address < t.instanceTrackers[j].address
	})

	// Update capacity
	t.updateCapacity(len(t.instanceTrackers))

	t.logger.Debug("Added instance", "address", address, "count", len(t.instanceTrackers), "capacity", t.breaker.Capacity())
}

// RemoveInstance removes an instance from the throttler.
// If the instance doesn't exist, this is a no-op.
func (t *Throttler) RemoveInstance(address string) {
	t.mux.Lock()
	defer t.mux.Unlock()

	// Find and remove the address
	for i, tracker := range t.instanceTrackers {
		if tracker.address == address {
			// Remove from slice by swapping with last element and truncating
			last := len(t.instanceTrackers) - 1
			t.instanceTrackers[i] = t.instanceTrackers[last]
			t.instanceTrackers = t.instanceTrackers[:last]

			// Sort for stable ordering
			sort.Slice(t.instanceTrackers, func(i, j int) bool {
				return t.instanceTrackers[i].address < t.instanceTrackers[j].address
			})

			// Update capacity
			t.updateCapacity(len(t.instanceTrackers))

			t.logger.Debug("Removed instance", "address", address, "count", len(t.instanceTrackers), "capacity", t.breaker.Capacity())
			return
		}
	}

	// Instance not found, no-op
	t.logger.Debug("Instance not found for removal", "address", address)
}

// updateCapacity updates the overall breaker capacity based on the number of instances.
func (t *Throttler) updateCapacity(instanceCount int) {
	var targetCapacity int
	if t.containerConcurrency == 0 {
		// For infinite CC, capacity is just 0 or 1 (no capacity or infinite capacity)
		if instanceCount > 0 {
			targetCapacity = 1
		} else {
			targetCapacity = 0
		}
	} else {
		// Capacity = CC * number of instances
		targetCapacity = t.containerConcurrency * instanceCount
		if targetCapacity > maxBreakerCapacity {
			targetCapacity = maxBreakerCapacity
		}
	}

	t.breaker.UpdateConcurrency(targetCapacity)
}

// acquireInstance returns an instance that has available capacity.
func (t *Throttler) acquireInstance(ctx context.Context) (func(), *instanceTracker) {
	t.mux.RLock()
	defer t.mux.RUnlock()

	cb, tracker := t.lbPolicy(ctx, t.instanceTrackers)
	return cb, tracker
}

// Try waits for capacity and then executes the function, passing the instance address.
// The function should make a GRPC call to the instance.
func (t *Throttler) Try(ctx context.Context, fn func(address string) error) error {
	var ret error

	// Retry loop to handle race conditions where capacity changes between
	// acquiring the outer semaphore and reserving an instance slot.
	reenqueue := true
	for reenqueue {
		reenqueue = false
		if err := t.breaker.Maybe(ctx, func() {
			cb, tracker := t.acquireInstance(ctx)
			if tracker == nil {
				// No instances available, reenqueue
				reenqueue = true
				return
			}
			defer cb()
			// Execute the function with the instance address
			ret = fn(tracker.address)
		}); err != nil {
			return err
		}
	}

	return ret
}

// GetInstanceCount returns the current number of instances.
func (t *Throttler) GetInstanceCount() int {
	t.mux.RLock()
	defer t.mux.RUnlock()
	return len(t.instanceTrackers)
}

// GetCapacity returns the current overall capacity.
func (t *Throttler) GetCapacity() int {
	return t.breaker.Capacity()
}
