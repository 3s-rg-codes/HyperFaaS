package net

import (
	"context"
	"sync/atomic"
)

// instanceTracker tracks a single function instance and its concurrency limits.
type instanceTracker struct {
	// address is the GRPC address of the instance (e.g., "host:port")
	address string

	// breaker enforces concurrency limits on this instance (nil if CC=0)
	breaker breaker

	// weight is used for load balancing policies
	weight atomic.Int32

	// decreaseWeight is called when releasing a reservation
	decreaseWeight func()
}

func newInstanceTracker(address string, b breaker) *instanceTracker {
	tracker := &instanceTracker{
		address: address,
		breaker: b,
	}
	tracker.decreaseWeight = func() { tracker.weight.Add(-1) }
	return tracker
}

func (it *instanceTracker) String() string {
	if it == nil {
		return "<nil>"
	}
	return it.address
}

// Capacity returns the current capacity of this instance.
func (it *instanceTracker) Capacity() int {
	if it.breaker == nil {
		return 1 // Infinite capacity when CC=0
	}
	return it.breaker.Capacity()
}

// UpdateConcurrency updates the concurrency limit for this instance.
func (it *instanceTracker) UpdateConcurrency(c int) {
	if it.breaker == nil {
		return
	}
	it.breaker.UpdateConcurrency(c)
}

// Reserve reserves a slot for a request to this instance.
func (it *instanceTracker) Reserve(ctx context.Context) (func(), bool) {
	if it.breaker == nil {
		return noop, true
	}
	return it.breaker.Reserve(ctx)
}

func (it *instanceTracker) increaseWeight() {
	it.weight.Add(1)
}

func (it *instanceTracker) getWeight() int32 {
	return it.weight.Load()
}
