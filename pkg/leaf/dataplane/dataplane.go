package dataplane

import (
	"context"
	"log/slog"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane/net"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
)

type mdclient interface {
	GetFunction(ctx context.Context, functionId string) (*metadata.FunctionMetadata, error)
}

// DataPlane manages throttlers for multiple functions.
// we use function id as keys instead of revision IDs like knative.
type DataPlane struct {
	throttlers map[string]*net.Throttler
	mu         sync.RWMutex
	logger     *slog.Logger

	mdClient mdclient
	// where we read instance changes from, so we can update the throttlers.
	instanceChangesChan chan metrics.InstanceChange

	// the concurrency reporter write metrics to.
	concurrencyReporter *metrics.ConcurrencyReporter
}

// NewDataPlane creates a new data plane.
func NewDataPlane(
	logger *slog.Logger,
	mdClient mdclient,
	instanceChangesChan chan metrics.InstanceChange,
	concurrencyReporter *metrics.ConcurrencyReporter,
) *DataPlane {
	return &DataPlane{
		throttlers:          make(map[string]*net.Throttler),
		logger:              logger,
		mdClient:            mdClient,
		instanceChangesChan: instanceChangesChan,
		concurrencyReporter: concurrencyReporter,
	}
}

func (m *DataPlane) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case instanceChange := <-m.instanceChangesChan:
			go func(ic metrics.InstanceChange) {
				m.UpdateFunctionInstances(ic)
			}(instanceChange)
		}
	}
}

// GetOrCreateThrottler gets or creates a throttler for a function.
func (m *DataPlane) GetOrCreateThrottler(functionId string) *net.Throttler {
	m.mu.RLock()
	throttler, ok := m.throttlers[functionId]
	m.mu.RUnlock()

	if ok {
		return throttler
	}

	m.mu.Lock()

	// Double-check after acquiring write lock.
	// This avoids the race condition where two goroutines create the throttler at the same time.
	throttler, ok = m.throttlers[functionId]
	if ok {
		m.mu.Unlock()
		return throttler
	}

	cc := m.getConfiguredMaxConcurrency(functionId)
	throttler = net.NewThrottler(int(cc), m.logger)
	m.throttlers[functionId] = throttler
	m.mu.Unlock()

	return throttler
}

// UpdateFunctionInstances updates the instances for a function.
// This should be called whenever the set of instances for a function changes.
func (m *DataPlane) UpdateFunctionInstances(ic metrics.InstanceChange) {
	throttler := m.GetOrCreateThrottler(ic.FunctionId)
	if ic.Have {
		m.logger.Debug("adding instance", "function_id", ic.FunctionId, "address", ic.Address)
		throttler.AddInstance(ic.Address)
	} else {
		throttler.RemoveInstance(ic.Address)
	}
}

// Try calls the throttler for a function and executes the callback with an instance address.
func (m *DataPlane) Try(
	ctx context.Context,
	functionId string,
	fn func(address string) error,
) error {
	throttler := m.GetOrCreateThrottler(functionId)
	m.concurrencyReporter.HandleRequestIn(functionId)
	defer m.concurrencyReporter.HandleRequestOut(functionId)
	return throttler.Try(ctx, fn)
}

// RemoveThrottler removes a throttler for a function (e.g., when function is deleted).
func (m *DataPlane) RemoveThrottler(functionId string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.throttlers, functionId)
}

// GetInstanceCount returns the number of instances for a function.
func (m *DataPlane) GetInstanceCount(functionId string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	throttler, ok := m.throttlers[functionId]
	if !ok {
		return 0
	}
	return throttler.GetInstanceCount()
}

func (m *DataPlane) getConfiguredMaxConcurrency(functionId string) int32 {
	funcMetadata, err := m.mdClient.GetFunction(context.Background(), functionId)
	if err != nil {
		return -1
	}
	return funcMetadata.Config.MaxConcurrency
}
