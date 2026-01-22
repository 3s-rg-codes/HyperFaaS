package controller

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mtrcs "github.com/3s-rg-codes/HyperFaaS/pkg/metrics"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
)

// ResourceManager is responsible for managing the resources of the worker.
// for now it's only useful to generate the simulated aggregate metrics for the worker.
// to calculate the aggregate usage, we sum up the worker's usage and the instances' usage.
// in a "bare metal" setup, this is of course not needed, as we can just work directly with the
// available system resources. But for the docker-compose setup, this gives us a way to simulate
// the aggregate usage of the worker.
// However, this manager might be useful to implement guards against starting instances when
// the worker is already at max capacity. Note that capacity != actual usage.
type ResourceManager struct {
	runtime      cr.ContainerRuntime
	logger       *slog.Logger
	interval     time.Duration
	budgetCPU    float64
	budgetMemory uint64

	mu         sync.Mutex
	containers map[string]*containerEntry
	updates    chan containerUpdate
	ctx        context.Context
	latest     atomic.Value
}

type containerEntry struct {
	lastUsage uint64
	prevUsage uint64
	lastMem   uint64
	cancel    context.CancelFunc
}

type containerUpdate struct {
	containerID string
	stats       cr.ContainerStats
}

func NewResourceManager(runtime cr.ContainerRuntime, logger *slog.Logger, interval time.Duration, budgetCPU float64, budgetMemory uint64) *ResourceManager {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &ResourceManager{
		runtime:      runtime,
		logger:       logger.With("component", "resource_manager"),
		interval:     interval,
		budgetCPU:    budgetCPU,
		budgetMemory: budgetMemory,
		containers:   make(map[string]*containerEntry),
		updates:      make(chan containerUpdate, 1024),
	}
}

func (r *ResourceManager) Run(ctx context.Context) {
	if r.ctx == nil {
		r.ctx = ctx
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-r.updates:
			r.mu.Lock()
			entry := r.containers[update.containerID]
			if entry != nil {
				entry.lastUsage = update.stats.CPUUsageTotal
				entry.lastMem = update.stats.MemoryUsage
			}
			r.mu.Unlock()
		case <-ticker.C:
			r.aggregate()
		}
	}
}

func (r *ResourceManager) SetContext(ctx context.Context) {
	r.ctx = ctx
}

func (r *ResourceManager) AddContainer(containerID string) {
	if containerID == "" {
		return
	}

	r.mu.Lock()
	if _, exists := r.containers[containerID]; exists {
		r.mu.Unlock()
		return
	}
	baseCtx := r.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	r.containers[containerID] = &containerEntry{cancel: cancel}
	r.mu.Unlock()

	statsCh, errCh := r.runtime.ContainerStats(ctx, containerID)
	go r.consumeStats(ctx, containerID, statsCh, errCh)
}

func (r *ResourceManager) RemoveContainer(containerID string) {
	if containerID == "" {
		return
	}
	r.mu.Lock()
	entry := r.containers[containerID]
	if entry == nil {
		for id, candidate := range r.containers {
			if len(containerID) <= len(id) && id[:len(containerID)] == containerID {
				entry = candidate
				containerID = id
				break
			}
		}
	}
	if entry != nil {
		entry.cancel()
		delete(r.containers, containerID)
	}
	r.mu.Unlock()
}

func (r *ResourceManager) Latest() (mtrcs.ResourceMetrics, bool) {
	value := r.latest.Load()
	if value == nil {
		return mtrcs.ResourceMetrics{}, false
	}
	metrics, ok := value.(mtrcs.ResourceMetrics)
	return metrics, ok
}

func (r *ResourceManager) consumeStats(ctx context.Context, containerID string, statsCh <-chan cr.ContainerStats, errCh <-chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		case stat, ok := <-statsCh:
			if !ok {
				return
			}
			select {
			case r.updates <- containerUpdate{containerID: containerID, stats: stat}:
			default:
				r.logger.Warn("resource manager buffer full", "container", containerID)
			}
		case err, ok := <-errCh:
			if ok && err != nil {
				r.logger.Debug("container stats stream ended", "container", containerID, "error", err)
			}
			return
		}
	}
}

func (r *ResourceManager) aggregate() {
	r.mu.Lock()
	defer r.mu.Unlock()

	var totalUsage uint64
	var totalMemory uint64
	for _, entry := range r.containers {
		if entry.lastUsage > 0 {
			if entry.prevUsage > 0 && entry.lastUsage >= entry.prevUsage {
				totalUsage += entry.lastUsage - entry.prevUsage
			}
			entry.prevUsage = entry.lastUsage
		}
		totalMemory += entry.lastMem
	}

	metrics := mtrcs.ResourceMetrics{
		CPUUtilizationRaw:    float64(totalUsage),
		MemoryUtilizationRaw: float64(totalMemory),
	}

	intervalSeconds := r.interval.Seconds()
	if r.budgetCPU > 0 && intervalSeconds > 0 {
		usageSeconds := float64(totalUsage) / 1e9
		usagePerCore := usageSeconds / intervalSeconds
		metrics.CPUUtilizationPercent = float32((usagePerCore / r.budgetCPU) * 100)
	}
	if r.budgetMemory > 0 {
		metrics.MemoryUtilizationPercent = float32((float64(totalMemory) / float64(r.budgetMemory)) * 100)
	}

	r.latest.Store(metrics)
}
