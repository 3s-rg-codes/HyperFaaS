package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane/net"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	functionpb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
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

	connPool *ConnPool
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
		connPool:            NewConnPool(),
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

func (m *DataPlane) CallWithConnPool(
	ctx context.Context,
	functionId string,
	req *common.CallRequest,
) (*common.CallResponse, error) {
	// TODO: In knative they only do HTTP so they dont have a return type.
	// but we actually need the response type so idk how to do it here without allocating
	// Investigate if this is a performance or memory bottleneck.

	var response *common.CallResponse

	err := m.Try(ctx, functionId, func(address string) error {
		conn, err := m.connPool.GetOrCreate(ctx, address)
		if err != nil {
			return err
		}

		client := functionpb.NewFunctionServiceClient(conn)
		resp, err := client.Call(ctx, req)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("dataplane failed to call function: %v", err)
	}
	return response, nil
}

// LeaseConnection reserves capacity for a function instance, returning a shared gRPC connection
// and a release function that MUST be called exactly once when the request is complete.
func (m *DataPlane) LeaseConnection(
	ctx context.Context,
	functionId string,
) (grpc.ClientConnInterface, func(error), error) {
	throttler := m.GetOrCreateThrottler(functionId)
	m.concurrencyReporter.HandleRequestIn(functionId)
	m.logger.Debug("leasing connection for function", "function_id", functionId)
	release, address, err := throttler.Lease(ctx)
	if err != nil {
		m.concurrencyReporter.HandleRequestOut(functionId)
		return nil, nil, err
	}
	m.logger.Debug("got address for function", "function_id", functionId, "address", address)
	conn, err := m.connPool.GetOrCreate(ctx, address)
	if err != nil {
		release()
		m.concurrencyReporter.HandleRequestOut(functionId)
		return nil, nil, err
	}
	m.logger.Debug("got connection for function", "function_id", functionId, "address", address)
	return conn, func(error) {
		release()
		m.concurrencyReporter.HandleRequestOut(functionId)
	}, nil
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
