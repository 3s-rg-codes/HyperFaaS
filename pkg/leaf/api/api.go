package api

import (
	"context"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	grpcpool "github.com/processout/grpc-go-pool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
	"sync"
	"time"
)

// grpc api endpoints that leafLeader will expose

// these will be called by Leaders above in the leader chain

// The main "state" of the leader is all of the worker IPs.
// Would only be changed if a worker is added or removed.

type LeafServer struct {
	leaf.UnimplementedLeafServer
	scheduler   scheduling.Scheduler
	poolManager PoolManager
}

func (s *LeafServer) ScheduleCall(ctx context.Context, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {

	workerID, instanceID, err := s.scheduler.Schedule(ctx, state.FunctionID(req.FunctionId))
	if err != nil {
		return nil, err
	}

	if instanceID == "" {
		// There is no idle instance available
		instanceID, err = s.startInstance(ctx, workerID, state.FunctionID(req.FunctionId))
		if err != nil {
			return nil, err
		}
		log.Printf("Started new instance for function %s on worker %s, instanceID: %s", req.FunctionId, workerID, instanceID)
		s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateNew)
	} else {
		// An Idle instance was found
		s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateRunning)
	}

	resp, err := s.callWorker(ctx, workerID, instanceID, req)
	if err != nil {
		return nil, err
	}

	// The instance is no longer running
	s.scheduler.UpdateInstanceState(workerID, state.FunctionID(req.FunctionId), instanceID, state.InstanceStateIdle)

	return resp, nil
}

func NewLeafServer(scheduler scheduling.Scheduler) *LeafServer {
	return &LeafServer{
		scheduler:   scheduler,
		poolManager: *NewPoolManager(), // Initialize PoolManager
	}
}

// TODO: refactor this to use a pool of connections.
// https://promisefemi.vercel.app/blog/grpc-client-connection-pooling
// https://github.com/processout/grpc-go-pool/blob/master/pool.go
func (s *LeafServer) callWorker(ctx context.Context, workerID state.WorkerID, instanceID state.InstanceID, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {

	log.Printf("[callWorker] Getting connection for worker %s", workerID)

	pool, err := s.poolManager.GetPool(string(workerID), func() (*grpc.ClientConn, error) {
		conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
		}
		return conn, nil
	})
	if err != nil {
		log.Printf("[callWorker] Failed to get connection pool for worker %s: %v", workerID, err)
		return nil, err
	}

	// Get a connection from the pool
	conn, err := pool.Get(ctx)
	if err != nil {
		log.Printf("Failed to get connection from pool for worker %s: %v", workerID, err)
		return nil, err
	}
	defer conn.Close() // Return connection to the pool
	log.Printf("[callWorker] Successfully reused connection for worker %s", workerID)

	// Create gRPC client using pooled connection
	client := controller.NewControllerClient(conn.ClientConn)

	// Prepare request
	callReq := &common.CallRequest{
		InstanceId: &common.InstanceID{Id: string(instanceID)},
		Data:       req.Data,
	}

	// Make the gRPC call
	resp, err := client.Call(ctx, callReq)
	if err != nil {
		log.Printf("gRPC call failed for worker %s: %v", workerID, err)
		return nil, err
	}

	log.Printf("[callWorker] Successfully called worker %s, reusing connection", workerID)
	// Return the response
	return &leaf.ScheduleCallResponse{Data: resp.Data, Error: resp.Error}, nil
}

func (s *LeafServer) startInstance(ctx context.Context, workerID state.WorkerID, functionId state.FunctionID) (state.InstanceID, error) {

	factory := func() (*grpc.ClientConn, error) {
		// Create a ClientConn using NewClient (same as in callWorker)
		conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
		}
		return conn, nil
	}

	// Get or create a connection pool for this worker
	pool, err := s.poolManager.GetPool(string(workerID), factory)
	if err != nil {
		return "", fmt.Errorf("failed to get connection pool for worker %s: %v", workerID, err)
	}

	// Get a connection from the pool
	conn, err := pool.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get connection from pool for worker %s: %v", workerID, err)
	}
	defer conn.Close() // Return connection to the pool

	client := controller.NewControllerClient(conn.ClientConn)

	// Todo : we need to agree on either functionId or imageTag
	startReq := &controller.StartRequest{
		ImageTag: &controller.ImageTag{Tag: string(functionId)},
	}

	instanceID, err := client.Start(ctx, startReq)
	if err != nil {
		return "", err
	}

	return state.InstanceID(instanceID.Id), nil
}

// PoolManager maintains pools for each worker
type PoolManager struct {
	mu    sync.RWMutex
	pools map[string]*grpcpool.Pool
}

// NewPoolManager creates a new PoolManager
func NewPoolManager() *PoolManager {
	return &PoolManager{
		pools: make(map[string]*grpcpool.Pool),
	}
}

// GetPool returns the pool for a given worker, creating one if it doesn't exist
func (pm *PoolManager) GetPool(workerID string, factory grpcpool.Factory) (*grpcpool.Pool, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if the pool already exists
	if pool, ok := pm.pools[workerID]; ok {
		log.Printf("[PoolManager] Reusing existing connection pool for worker %s", workerID)
		return pool, nil
	}

	// Create a new pool
	pool, err := grpcpool.New(factory, 1, 7, 30*time.Second)
	if err != nil {
		return nil, err
	}

	log.Printf("[PoolManager] Created new connection pool for worker %s", workerID)
	pm.pools[workerID] = pool
	return pool, nil
}

func (s *LeafServer) Stop(ctx context.Context, req *leaf.StopRequest) (*leaf.StopResponse, error) {
	s.poolManager.mu.Lock()
	defer s.poolManager.mu.Unlock()

	if s.poolManager.pools == nil {
		log.Printf("PoolManager already closed. Ignoring redundant stop call.")
		return &leaf.StopResponse{}, nil
	}
	// Close all connection pools
	for workerID, pool := range s.poolManager.pools {
		log.Printf("Closing connection pool for worker: %s", workerID)
		pool.Close()
	}
	// Clear the pools map safely
	s.poolManager.pools = make(map[string]*grpcpool.Pool)

	return &leaf.StopResponse{}, nil
}
