package metrics

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mtrcs "github.com/3s-rg-codes/HyperFaaS/pkg/metrics"
)

const (
	ResourceMetricsWindow   = 60
	ResourceMetricsBuffer   = 10000
	ResourceMetricsInterval = time.Second
)

type BestWorkerSnapshot struct {
	Index   int
	Metrics mtrcs.ResourceMetrics
	Ok      bool
}

type ResourceMetricEvent struct {
	WorkerIdx int
	Metrics   mtrcs.ResourceMetrics
}

type ResourceMetricsStore struct {
	workers []workerMetricsRing
	locks   []sync.RWMutex
	latest  []atomic.Value
	best    atomic.Value
}

// Latest reads are lock-free and can be slightly stale while a tick is in-flight.
func NewResourceMetricsStore(workerCount int) *ResourceMetricsStore {
	if workerCount < 0 {
		workerCount = 0
	}
	store := &ResourceMetricsStore{
		workers: make([]workerMetricsRing, workerCount),
		locks:   make([]sync.RWMutex, workerCount),
		latest:  make([]atomic.Value, workerCount),
	}
	return store
}

func (s *ResourceMetricsStore) WorkerCount() int {
	return len(s.workers)
}

func (s *ResourceMetricsStore) Update(workerIdx int, metrics mtrcs.ResourceMetrics) {
	if workerIdx < 0 || workerIdx >= len(s.workers) {
		return
	}
	lock := &s.locks[workerIdx]
	lock.Lock()
	s.workers[workerIdx].append(metrics)
	lock.Unlock()
	s.latest[workerIdx].Store(metrics)
}

func (s *ResourceMetricsStore) Latest(workerIdx int) (mtrcs.ResourceMetrics, bool) {
	if workerIdx < 0 || workerIdx >= len(s.latest) {
		return mtrcs.ResourceMetrics{}, false
	}
	value := s.latest[workerIdx].Load()
	if value == nil {
		return mtrcs.ResourceMetrics{}, false
	}
	metrics, ok := value.(mtrcs.ResourceMetrics)
	return metrics, ok
}

func (s *ResourceMetricsStore) History(workerIdx int, dst []mtrcs.ResourceMetrics) int {
	if workerIdx < 0 || workerIdx >= len(s.workers) {
		return 0
	}
	lock := &s.locks[workerIdx]
	lock.RLock()
	n := s.workers[workerIdx].history(dst)
	lock.RUnlock()
	return n
}

func (s *ResourceMetricsStore) SetBest(snapshot BestWorkerSnapshot) {
	s.best.Store(snapshot)
}

func (s *ResourceMetricsStore) BestWorker() (BestWorkerSnapshot, bool) {
	value := s.best.Load()
	if value == nil {
		return BestWorkerSnapshot{}, false
	}
	snapshot, ok := value.(BestWorkerSnapshot)
	return snapshot, ok
}

type ResourceMetricsCollector struct {
	store    *ResourceMetricsStore
	updates  chan ResourceMetricEvent
	interval time.Duration
	logger   *slog.Logger
}

type pendingMetric struct {
	set     bool
	metrics mtrcs.ResourceMetrics
}

func NewResourceMetricsCollector(store *ResourceMetricsStore, logger *slog.Logger, interval time.Duration) *ResourceMetricsCollector {
	if interval <= 0 {
		interval = ResourceMetricsInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ResourceMetricsCollector{
		store:    store,
		updates:  make(chan ResourceMetricEvent, ResourceMetricsBuffer),
		interval: interval,
		logger:   logger.With("component", "resource_metrics"),
	}
}

func (c *ResourceMetricsCollector) Add(workerIdx int, metrics mtrcs.ResourceMetrics) {
	select {
	case c.updates <- ResourceMetricEvent{WorkerIdx: workerIdx, Metrics: metrics}:
	default:
		c.logger.Warn("resource metrics buffer full", "worker", workerIdx)
	}
}

func (c *ResourceMetricsCollector) Run(ctx context.Context) {
	if c.store == nil {
		return
	}
	pending := make([]pendingMetric, c.store.WorkerCount())
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.updates:
			if ev.WorkerIdx < 0 || ev.WorkerIdx >= len(pending) {
				continue
			}
			pending[ev.WorkerIdx] = pendingMetric{set: true, metrics: ev.Metrics}
		case <-ticker.C:
			c.flushPending(pending)
		}
	}
}

func (c *ResourceMetricsCollector) flushPending(pending []pendingMetric) {
	for idx := range pending {
		if pending[idx].set {
			c.store.Update(idx, pending[idx].metrics)
			pending[idx] = pendingMetric{}
		}
	}

	best := BestWorkerSnapshot{}
	for idx := 0; idx < c.store.WorkerCount(); idx++ {
		metrics, ok := c.store.Latest(idx)
		if !ok {
			continue
		}
		if !best.Ok || metrics.CPUUtilizationPercent < best.Metrics.CPUUtilizationPercent {
			// c.logger.Info("new best worker", "worker", idx, "cpu_percent", metrics.CPUUtilizationPercent, "memory_percent", metrics.MemoryUtilizationPercent)
			best = BestWorkerSnapshot{Index: idx, Metrics: metrics, Ok: true}
		}
	}
	if best.Ok {
		c.store.SetBest(best)
	}
}

type workerMetricsRing struct {
	head   int
	count  int
	cpuRaw [ResourceMetricsWindow]float64
	cpuPct [ResourceMetricsWindow]float32
	memRaw [ResourceMetricsWindow]float64
	memPct [ResourceMetricsWindow]float32
}

func (w *workerMetricsRing) append(metrics mtrcs.ResourceMetrics) {
	w.cpuRaw[w.head] = metrics.CPUUtilizationRaw
	w.cpuPct[w.head] = metrics.CPUUtilizationPercent
	w.memRaw[w.head] = metrics.MemoryUtilizationRaw
	w.memPct[w.head] = metrics.MemoryUtilizationPercent
	w.head = (w.head + 1) % ResourceMetricsWindow
	if w.count < ResourceMetricsWindow {
		w.count++
	}
}

func (w *workerMetricsRing) history(dst []mtrcs.ResourceMetrics) int {
	if w.count == 0 || len(dst) == 0 {
		return 0
	}
	n := w.count
	if len(dst) < n {
		n = len(dst)
	}
	start := w.head - w.count
	if start < 0 {
		start += ResourceMetricsWindow
	}
	for i := 0; i < n; i++ {
		idx := (start + i) % ResourceMetricsWindow
		dst[i] = mtrcs.ResourceMetrics{
			CPUUtilizationRaw:        w.cpuRaw[idx],
			CPUUtilizationPercent:    w.cpuPct[idx],
			MemoryUtilizationRaw:     w.memRaw[idx],
			MemoryUtilizationPercent: w.memPct[idx],
		}
	}
	return n
}
