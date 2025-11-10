package net

import (
	"context"
	"math/rand"
	"sync"
)

// lbPolicy is a function that selects a target instance from the list,
// or returns (noop, nil) if no target can be currently acquired.
type lbPolicy func(ctx context.Context, targets []*instanceTracker) (func(), *instanceTracker)

// randomLBPolicy picks a random target instance.
func randomLBPolicy(_ context.Context, targets []*instanceTracker) (func(), *instanceTracker) {
	if len(targets) == 0 {
		return noop, nil
	}
	return noop, targets[rand.Intn(len(targets))]
}

// randomChoice2Policy implements the Power of 2 choices load balancing algorithm.
func randomChoice2Policy(_ context.Context, targets []*instanceTracker) (func(), *instanceTracker) {
	l := len(targets)
	if l == 0 {
		return noop, nil
	}
	if l == 1 {
		pick := targets[0]
		pick.increaseWeight()
		return pick.decreaseWeight, pick
	}

	r1, r2 := 0, 1
	if l > 2 {
		r1 = rand.Intn(l)
		r2 = rand.Intn(l - 1)
		if r2 >= r1 {
			r2++
		}
	}

	pick, alt := targets[r1], targets[r2]
	if pick.getWeight() > alt.getWeight() {
		pick = alt
	} else if pick.getWeight() == alt.getWeight() {
		if rand.Int63()%2 == 0 {
			pick = alt
		}
	}
	pick.increaseWeight()
	return pick.decreaseWeight, pick
}

// firstAvailableLBPolicy picks the first target that has capacity to serve the request.
func firstAvailableLBPolicy(ctx context.Context, targets []*instanceTracker) (func(), *instanceTracker) {
	if len(targets) == 0 {
		return noop, nil
	}
	for _, t := range targets {
		if cb, ok := t.Reserve(ctx); ok {
			return cb, t
		}
	}
	return noop, nil
}

// roundRobinLBPolicy implements round-robin load balancing.
func roundRobinLBPolicy() lbPolicy {
	var (
		mu  sync.Mutex
		idx int
	)
	return func(ctx context.Context, targets []*instanceTracker) (func(), *instanceTracker) {
		mu.Lock()
		defer mu.Unlock()

		l := len(targets)
		if l == 0 {
			return noop, nil
		}
		if idx >= l {
			idx = 0
		}

		for i := range l {
			p := (idx + i) % l
			if cb, ok := targets[p].Reserve(ctx); ok {
				idx = p + 1
				return cb, targets[p]
			}
		}

		return noop, nil
	}
}
