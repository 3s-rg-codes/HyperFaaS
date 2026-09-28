package dataplane

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultSandboxConcurrency = 200
	DefaultQueueDepth         = 10_000
)

var (
	ErrQueueFull  = errors.New("sandbox pool queue full")
	ErrPoolClosed = errors.New("sandbox pool closed")
	ErrNoSandbox  = errors.New("sandbox not found")
)

// Sandbox is the complete immutable request target for one ready sandbox.
type Sandbox struct {
	InstanceID uint64
	WorkerID   uint64
	Address    string
	Protocol   string
}

type pooledSandbox struct {
	target   Sandbox
	inFlight int
	ready    *list.Element
	draining bool
	drained  chan struct{}
}

type waiter struct {
	ctx    context.Context
	result chan acquireResult
	elem   *list.Element
}

type acquireResult struct {
	sandbox *pooledSandbox
	err     error
}

// Lease represents one reserved sandbox slot. Release is safe to call more
// than once, including concurrently: only the first call changes pool state.
type Lease struct {
	pool     *SandboxPool
	sandbox  *pooledSandbox
	released uint32
}

func (l *Lease) Sandbox() Sandbox {
	if l == nil || l.sandbox == nil {
		return Sandbox{}
	}
	return l.sandbox.target
}

func (l *Lease) Release() {
	if l == nil || l.pool == nil || l.sandbox == nil {
		return
	}
	if !atomic.CompareAndSwapUint32(&l.released, 0, 1) {
		return
	}
	l.pool.release(l.sandbox)
}

// PoolSnapshot is the complete request-time state used by routing and scaling.
type PoolSnapshot struct {
	ReadyInstances       int
	AvailableConcurrency uint64
	Executing            int
	Queued               int
	Accepted             int64
	Completed            int64
	HighWater            int64
	LastActivity         time.Time
	OldestQueued         time.Time
}

// SandboxPool is the only source of truth for ready sandbox targets and their
// request capacity for one function.
type SandboxPool struct {
	mu         sync.Mutex
	limit      int
	queueDepth int
	sandboxes  map[uint64]*pooledSandbox
	ready      list.List
	pending    list.List
	active     int
	executing  int
	closed     bool
	queueHead  time.Time
	demandWake chan<- struct{}

	accepted     atomic.Int64
	completed    atomic.Int64
	highWater    atomic.Int64
	lastActivity atomic.Int64
}

func NewSandboxPool(limit, queueDepth int) *SandboxPool {
	p := &SandboxPool{sandboxes: make(map[uint64]*pooledSandbox)}
	p.Configure(limit, queueDepth)
	return p
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultSandboxConcurrency
	}
	return limit
}

func (p *SandboxPool) Configure(limit, queueDepth int) {
	limit = normalizeLimit(limit)
	if queueDepth <= 0 {
		queueDepth = DefaultQueueDepth
	}
	p.mu.Lock()
	p.limit = limit
	p.queueDepth = queueDepth
	for _, sandbox := range p.sandboxes {
		p.refreshReadyLocked(sandbox)
	}
	p.dispatchLocked()
	p.mu.Unlock()
}

func (p *SandboxPool) SetDemandWake(wake chan<- struct{}) {
	p.mu.Lock()
	p.demandWake = wake
	hasDemand := p.executing+p.pending.Len() > 0
	p.mu.Unlock()
	if hasDemand {
		notify(wake)
	}
}

func (p *SandboxPool) Add(target Sandbox) {
	if target.InstanceID == 0 || target.Address == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if current := p.sandboxes[target.InstanceID]; current != nil {
		current.target = target
		p.refreshReadyLocked(current)
		return
	}
	sandbox := &pooledSandbox{target: target}
	p.sandboxes[target.InstanceID] = sandbox
	p.active++
	p.refreshReadyLocked(sandbox)
	p.dispatchLocked()
}

func (p *SandboxPool) Sync(targets []Sandbox) {
	wanted := make(map[uint64]Sandbox, len(targets))
	for _, target := range targets {
		if target.InstanceID != 0 && target.Address != "" {
			wanted[target.InstanceID] = target
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	for id, sandbox := range p.sandboxes {
		if _, keep := wanted[id]; keep || sandbox.draining {
			continue
		}
		p.removeReadyLocked(sandbox)
		delete(p.sandboxes, id)
		p.active--
	}
	for id, target := range wanted {
		if sandbox := p.sandboxes[id]; sandbox != nil {
			sandbox.target = target
			p.refreshReadyLocked(sandbox)
			continue
		}
		sandbox := &pooledSandbox{target: target}
		p.sandboxes[id] = sandbox
		p.active++
		p.refreshReadyLocked(sandbox)
	}
	p.dispatchLocked()
}

func (p *SandboxPool) Remove(instanceID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sandbox := p.sandboxes[instanceID]
	if sandbox == nil {
		return
	}
	p.removeReadyLocked(sandbox)
	if !sandbox.draining {
		p.active--
	}
	delete(p.sandboxes, instanceID)
}

// Drain prevents new leases and waits for existing leases to finish. The
// returned function commits or cancels removal after the external stop call.
func (p *SandboxPool) Drain(ctx context.Context, instanceID uint64) (func(bool), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	sandbox := p.sandboxes[instanceID]
	if sandbox == nil || sandbox.draining {
		p.mu.Unlock()
		return nil, ErrNoSandbox
	}
	sandbox.draining = true
	p.active--
	p.removeReadyLocked(sandbox)
	sandbox.drained = make(chan struct{})
	if sandbox.inFlight == 0 {
		close(sandbox.drained)
	}
	drained := sandbox.drained
	p.mu.Unlock()

	select {
	case <-drained:
	case <-ctx.Done():
		p.restore(sandbox)
		return nil, ctx.Err()
	}

	var once sync.Once
	return func(stopped bool) {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.sandboxes[instanceID] != sandbox || !sandbox.draining {
				return
			}
			if stopped {
				delete(p.sandboxes, instanceID)
				return
			}
			sandbox.draining = false
			sandbox.drained = nil
			p.active++
			p.refreshReadyLocked(sandbox)
			p.dispatchLocked()
		})
	}, nil
}

func (p *SandboxPool) restore(sandbox *pooledSandbox) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sandboxes[sandbox.target.InstanceID] != sandbox || !sandbox.draining {
		return
	}
	sandbox.draining = false
	sandbox.drained = nil
	p.active++
	p.refreshReadyLocked(sandbox)
	p.dispatchLocked()
}

func (p *SandboxPool) Acquire(ctx context.Context) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return Lease{}, ErrPoolClosed
	}
	if p.pending.Len() == 0 {
		if sandbox := p.acquireLocked(); sandbox != nil {
			p.mu.Unlock()
			return Lease{pool: p, sandbox: sandbox}, nil
		}
	}
	if p.pending.Len() >= p.queueDepth {
		p.mu.Unlock()
		return Lease{}, ErrQueueFull
	}
	wasIdle := p.executing == 0 && p.pending.Len() == 0
	w := &waiter{ctx: ctx, result: make(chan acquireResult, 1)}
	w.elem = p.pending.PushBack(w)
	firstQueued := p.pending.Len() == 1
	if p.queueHead.IsZero() {
		p.queueHead = time.Now()
	}
	p.noteDemandLocked()
	wake := p.demandWake
	p.mu.Unlock()
	if wasIdle || firstQueued {
		notify(wake)
	}

	select {
	case result := <-w.result:
		if result.err != nil {
			return Lease{}, result.err
		}
		if err := ctx.Err(); err != nil {
			p.release(result.sandbox)
			return Lease{}, err
		}
		return Lease{pool: p, sandbox: result.sandbox}, nil
	case <-ctx.Done():
		p.mu.Lock()
		if w.elem != nil {
			p.pending.Remove(w.elem)
			w.elem = nil
			p.updateQueueHeadLocked()
			p.mu.Unlock()
			return Lease{}, ctx.Err()
		}
		p.mu.Unlock()
		result := <-w.result
		if result.sandbox != nil {
			p.release(result.sandbox)
		}
		return Lease{}, ctx.Err()
	}
}

func (p *SandboxPool) acquireLocked() *pooledSandbox {
	elem := p.ready.Front()
	if elem == nil {
		return nil
	}
	sandbox := elem.Value.(*pooledSandbox)
	sandbox.inFlight++
	p.executing++
	p.accepted.Add(1)
	if p.executing == 1 && p.pending.Len() == 0 {
		p.lastActivity.Store(time.Now().UnixNano())
	}
	if sandbox.inFlight >= p.limit {
		p.removeReadyLocked(sandbox)
	} else {
		p.ready.MoveToBack(elem)
	}
	p.noteDemandLocked()
	return sandbox
}

func (p *SandboxPool) release(sandbox *pooledSandbox) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sandbox.inFlight <= 0 {
		panic("sandbox pool: release without lease")
	}
	sandbox.inFlight--
	p.executing--
	p.completed.Add(1)
	if p.executing == 0 && p.pending.Len() == 0 {
		p.lastActivity.Store(time.Now().UnixNano())
	}
	if p.closed {
		return
	}
	if sandbox.draining {
		if sandbox.inFlight == 0 && sandbox.drained != nil {
			close(sandbox.drained)
		}
		return
	}
	if p.sandboxes[sandbox.target.InstanceID] == sandbox {
		p.refreshReadyLocked(sandbox)
		p.dispatchLocked()
	}
}

func (p *SandboxPool) dispatchLocked() {
	for p.pending.Len() > 0 {
		front := p.pending.Front()
		w := front.Value.(*waiter)
		if err := w.ctx.Err(); err != nil {
			p.pending.Remove(front)
			w.elem = nil
			w.result <- acquireResult{err: err}
			continue
		}
		sandbox := p.acquireLocked()
		if sandbox == nil {
			return
		}
		p.pending.Remove(front)
		w.elem = nil
		p.updateQueueHeadLocked()
		w.result <- acquireResult{sandbox: sandbox}
	}
}

func (p *SandboxPool) refreshReadyLocked(sandbox *pooledSandbox) {
	available := !sandbox.draining && sandbox.inFlight < p.limit
	if available && sandbox.ready == nil {
		sandbox.ready = p.ready.PushBack(sandbox)
	} else if !available {
		p.removeReadyLocked(sandbox)
	}
}

func (p *SandboxPool) removeReadyLocked(sandbox *pooledSandbox) {
	if sandbox.ready == nil {
		return
	}
	p.ready.Remove(sandbox.ready)
	sandbox.ready = nil
}

func (p *SandboxPool) updateQueueHeadLocked() {
	if p.pending.Len() == 0 {
		p.queueHead = time.Time{}
	}
}

func (p *SandboxPool) noteDemandLocked() {
	demand := int64(p.executing + p.pending.Len())
	for {
		high := p.highWater.Load()
		if high >= demand || p.highWater.CompareAndSwap(high, demand) {
			return
		}
	}
}

func (p *SandboxPool) Snapshot() PoolSnapshot {
	p.mu.Lock()
	ready := p.active
	executing := p.executing
	queued := p.pending.Len()
	oldest := p.queueHead
	available := 0
	for _, sandbox := range p.sandboxes {
		if !sandbox.draining && sandbox.inFlight < p.limit {
			available += p.limit - sandbox.inFlight
		}
	}
	p.mu.Unlock()
	return PoolSnapshot{
		ReadyInstances:       ready,
		AvailableConcurrency: uint64(available),
		Executing:            executing,
		Queued:               queued,
		Accepted:             p.accepted.Load(),
		Completed:            p.completed.Load(),
		HighWater:            p.highWater.Load(),
		LastActivity:         timeFromUnixNano(p.lastActivity.Load()),
		OldestQueued:         oldest,
	}
}

func (p *SandboxPool) TakeHighWater() int64 {
	p.mu.Lock()
	current := int64(p.executing + p.pending.Len())
	high := p.highWater.Swap(current)
	p.mu.Unlock()
	if high > current {
		return high
	}
	return current
}

func (p *SandboxPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.ready.Init()
	for p.pending.Len() > 0 {
		front := p.pending.Front()
		w := front.Value.(*waiter)
		p.pending.Remove(front)
		w.elem = nil
		w.result <- acquireResult{err: ErrPoolClosed}
	}
}

func notify(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func timeFromUnixNano(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value)
}
