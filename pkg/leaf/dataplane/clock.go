package dataplane

import (
	"sync/atomic"
	"time"
)

// coarseClock is a process-wide, low-cost wall clock. One background ticker
// refreshes it, so request-path code reads an atomic instead of calling
// time.Now. Admission uses it for activity and queue-age timestamps, where
// millisecond accuracy is irrelevant next to multi-second timeouts.
type coarseClock struct {
	nanos atomic.Int64
}

const coarseClockInterval = 20 * time.Millisecond

var leafClock = newCoarseClock()

func newCoarseClock() *coarseClock {
	c := &coarseClock{}
	c.nanos.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(coarseClockInterval)
		defer t.Stop()
		for range t.C {
			c.nanos.Store(time.Now().UnixNano())
		}
	}()
	return c
}

func (c *coarseClock) nowUnixNano() int64 { return c.nanos.Load() }
