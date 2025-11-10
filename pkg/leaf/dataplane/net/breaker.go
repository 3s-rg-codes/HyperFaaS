package net

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
)

const (
	// Default queue depth for breakers
	defaultQueueDepth = 10000
	// Max capacity for breakers (limited by chan struct{} size)
	maxBreakerCapacity = math.MaxInt32
)

// BreakerParams defines the parameters of the breaker.
type BreakerParams struct {
	QueueDepth      int
	MaxConcurrency  int
	InitialCapacity int
}

// breaker is the interface that all breaker implementations must satisfy.
type breaker interface {
	Capacity() int
	Maybe(ctx context.Context, thunk func()) error
	UpdateConcurrency(int)
	Reserve(ctx context.Context) (func(), bool)
}

// Breaker is a component that enforces a concurrency limit on the
// execution of a function. It also maintains a queue of function
// executions in excess of the concurrency limit.
type Breaker struct {
	inFlight   atomic.Int64
	totalSlots int64
	sem        *semaphore
	release    func()
}

// NewBreaker creates a Breaker with the desired queue depth,
// concurrency limit and initial capacity.
func NewBreaker(params BreakerParams) *Breaker {
	if params.QueueDepth <= 0 {
		params.QueueDepth = defaultQueueDepth
	}
	if params.MaxConcurrency < 0 {
		panic("Max concurrency must be 0 or greater")
	}
	if params.InitialCapacity < 0 || params.InitialCapacity > params.MaxConcurrency {
		params.InitialCapacity = params.MaxConcurrency
	}

	b := &Breaker{
		totalSlots: int64(params.QueueDepth + params.MaxConcurrency),
		sem:        newSemaphore(params.MaxConcurrency, params.InitialCapacity),
	}

	b.release = func() {
		b.sem.release()
		b.releasePending()
	}

	return b
}

func (b *Breaker) tryAcquirePending() bool {
	for {
		cur := b.inFlight.Load()
		if cur == b.totalSlots {
			return false
		}
		if b.inFlight.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (b *Breaker) releasePending() {
	b.inFlight.Add(-1)
}

// Reserve reserves an execution slot in the breaker.
func (b *Breaker) Reserve(ctx context.Context) (func(), bool) {
	if !b.tryAcquirePending() {
		return nil, false
	}

	if !b.sem.tryAcquire() {
		b.releasePending()
		return nil, false
	}

	return b.release, true
}

// Maybe conditionally executes thunk based on the Breaker concurrency
// and queue parameters.
func (b *Breaker) Maybe(ctx context.Context, thunk func()) error {
	if !b.tryAcquirePending() {
		return ErrRequestQueueFull
	}

	defer b.releasePending()

	if err := b.sem.acquire(ctx); err != nil {
		return err
	}
	defer b.sem.release()

	thunk()
	return nil
}

// Capacity returns the number of allowed in-flight requests on this breaker.
func (b *Breaker) Capacity() int {
	return b.sem.Capacity()
}

// UpdateConcurrency updates the maximum number of in-flight requests.
func (b *Breaker) UpdateConcurrency(size int) {
	b.sem.updateCapacity(size)
}

var ErrRequestQueueFull = &queueFullError{}

type queueFullError struct{}

func (e *queueFullError) Error() string {
	return "pending request queue full"
}

// semaphore is an implementation of a semaphore based on packed integers and a channel.
type semaphore struct {
	state atomic.Uint64
	queue chan struct{}
}

func newSemaphore(maxCapacity, initialCapacity int) *semaphore {
	queue := make(chan struct{}, maxCapacity)
	sem := &semaphore{queue: queue}
	sem.updateCapacity(initialCapacity)
	return sem
}

func (s *semaphore) tryAcquire() bool {
	for {
		old := s.state.Load()
		capacity, in := unpack(old)
		if in >= capacity {
			return false
		}
		in++
		if s.state.CompareAndSwap(old, pack(capacity, in)) {
			return true
		}
	}
}

func (s *semaphore) acquire(ctx context.Context) error {
	for {
		old := s.state.Load()
		capacity, in := unpack(old)

		if in >= capacity {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.queue:
			}
			continue
		}

		in++
		if s.state.CompareAndSwap(old, pack(capacity, in)) {
			return nil
		}
	}
}

func (s *semaphore) release() {
	for {
		old := s.state.Load()
		capacity, in := unpack(old)

		if in == 0 {
			panic("release and acquire are not paired")
		}

		in--
		if s.state.CompareAndSwap(old, pack(capacity, in)) {
			if in < capacity {
				select {
				case s.queue <- struct{}{}:
				default:
				}
			}
			return
		}
	}
}

func (s *semaphore) updateCapacity(size int) {
	s64 := uint64(size)
	for {
		old := s.state.Load()
		capacity, in := unpack(old)

		if capacity == s64 {
			return
		}

		if s.state.CompareAndSwap(old, pack(s64, in)) {
			if s64 > capacity {
				for range s64 - capacity {
					select {
					case s.queue <- struct{}{}:
					default:
					}
				}
			}
			return
		}
	}
}

func (s *semaphore) Capacity() int {
	capacity, _ := unpack(s.state.Load())
	return int(capacity)
}

func unpack(in uint64) (uint64, uint64) {
	return in >> 32, in & 0xffffffff
}

func pack(left, right uint64) uint64 {
	return left<<32 | right
}

// infiniteBreaker provides capability to send unlimited number of requests.
// Used when container concurrency is 0 (unlimited).
// The infiniteBreaker will block requests when downstream capacity is 0.
type infiniteBreaker struct {
	mu sync.RWMutex

	// broadcast channel is used to notify waiting requests that downstream capacity appeared.
	// When capacity switches from 0 to 1, the channel is closed.
	// When capacity disappears, a new channel is created.
	broadcast chan struct{}

	// concurrency takes only two values: 0 (no capacity) or 1 (infinite capacity).
	concurrency atomic.Int32

	logger *slog.Logger
}

func newInfiniteBreaker(logger *slog.Logger) *infiniteBreaker {
	return &infiniteBreaker{
		broadcast: make(chan struct{}),
		logger:    logger,
	}
}

func (ib *infiniteBreaker) Capacity() int {
	return int(ib.concurrency.Load())
}

func zeroOrOne(x int) int32 {
	if x == 0 {
		return 0
	}
	return 1
}

func (ib *infiniteBreaker) UpdateConcurrency(cc int) {
	rcc := zeroOrOne(cc)
	ib.mu.Lock()
	defer ib.mu.Unlock()
	old := ib.concurrency.Swap(rcc)

	if old != rcc {
		if rcc == 0 {
			ib.broadcast = make(chan struct{})
		} else {
			close(ib.broadcast)
		}
	}
}

func (ib *infiniteBreaker) Maybe(ctx context.Context, thunk func()) error {
	has := ib.Capacity()
	if has > 0 {
		thunk()
		return nil
	}

	var ch chan struct{}
	ib.mu.RLock()
	ch = ib.broadcast
	ib.mu.RUnlock()
	select {
	case <-ch:
		thunk()
		return nil
	case <-ctx.Done():
		if ib.logger != nil {
			ib.logger.Debug("Context canceled", "error", ctx.Err())
		}
		return ctx.Err()
	}
}

func (ib *infiniteBreaker) Reserve(context.Context) (func(), bool) {
	return noop, true
}

func noop() {}
