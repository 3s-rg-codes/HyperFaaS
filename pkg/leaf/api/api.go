package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerPB "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	grpcpool "github.com/processout/grpc-go-pool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type LeafServer struct {
	leaf.UnimplementedLeafServer
	scheduler                       scheduling.Scheduler
	database                        kv.FunctionMetadataStore
	functionIdCache                 map[string]kv.FunctionData
	poolManager                     PoolManager
	coordinator                     *InstanceCoordinator
	maxStartingInstancesPerFunction int
	startingInstanceWaitTimeout     time.Duration
	maxRunningInstancesPerFunction  int
}

type CallMetadata struct {
	CallQueuedTimestamp        string
	GotResponseTimestamp       string
	FunctionProcessingTime     string
	LeafGotRequestTimestamp    string
	LeafScheduledCallTimestamp string
}

// CreateFunction should only create the function, e.g. save its Config and image tag in local cache
func (s *LeafServer) CreateFunction(ctx context.Context, req *leaf.CreateFunctionRequest) (*leaf.CreateFunctionResponse, error) {

	functionID, err := s.database.Put(req.ImageTag, req.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to store function in database: %w", err)
	}

	s.functionIdCache[functionID.Id] = kv.FunctionData{
		Config:   req.Config, //Also needed here for scheduling decisions
		ImageTag: req.ImageTag,
	}

	return &leaf.CreateFunctionResponse{
		FunctionID: functionID,
	}, nil

}

// ScheduleCall places a call to a function on a worker and returns the response
func (s *LeafServer) ScheduleCall(ctx context.Context, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {
	leafGotRequestTimestamp := time.Now()
	if _, ok := s.functionIdCache[req.FunctionID.Id]; !ok {
		ImageTag, Config, err := s.database.Get(req.FunctionID)
		if err != nil {
			var noSuchKeyError kv.NoSuchKeyError
			if errors.As(err, &noSuchKeyError) {
				return nil, status.Errorf(codes.NotFound, "failed to get function from database: %s", req.FunctionID.Id)
			}
			return nil, fmt.Errorf("failed to get function from database: %w", err)
		}

		s.functionIdCache[req.FunctionID.Id] = kv.FunctionData{
			Config:   Config,
			ImageTag: ImageTag,
		}
	}

	functionId := state.FunctionID(req.FunctionID.Id)

	// Use the coordinator to handle instance creation with synchronization
	workerID, instanceID, err := s.coordinator.CoordinateInstanceCreation(
		ctx,
		functionId,
		s.IsFunctionBackpressured,
		s.scheduler.Schedule,
		s.startInstance,
		func(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, state state.InstanceState) {
			s.scheduler.UpdateInstanceState(workerID, functionID, instanceID, state)
		},
		s.startingInstanceWaitTimeout,
	)
	if err != nil {
		return nil, err
	}

	leafScheduledCallTimestamp := time.Now()

	resp, callMetadata, err := s.callWorker(ctx, workerID, functionId, instanceID, req)
	if err != nil {
		switch err.(type) {
		case *controller.ContainerCrashError, *controller.InstanceNotFoundError:
			s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateDown)
			return nil, err
		case *WorkerDownError:
			s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateDown)
			s.scheduler.UpdateWorkerState(workerID, state.WorkerStateDown)
			return nil, err
		default:
			return nil, err
		}
	}

	//log.Printf("Received response from worker %s, instanceID: %s", workerID, instanceID)
	s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateIdle)

	// Add metadata to trailers
	trailer := metadata.New(map[string]string{
		"callQueuedTimestamp":        callMetadata.CallQueuedTimestamp,
		"gotResponseTimestamp":       callMetadata.GotResponseTimestamp,
		"functionProcessingTime":     callMetadata.FunctionProcessingTime,
		"instanceID":                 string(instanceID),
		"leafGotRequestTimestamp":    strconv.FormatInt(leafGotRequestTimestamp.UnixNano(), 10),
		"leafScheduledCallTimestamp": strconv.FormatInt(leafScheduledCallTimestamp.UnixNano(), 10),
	})
	grpc.SetTrailer(ctx, trailer)

	return resp, nil
}

func NewLeafServer(
	scheduler scheduling.Scheduler,
	httpClient kv.FunctionMetadataStore,
	maxStartingInstancesPerFunction int,
	startingInstanceWaitTimeout time.Duration,
	maxRunningInstancesPerFunction int,
	coordinatorBackoff time.Duration,
	coordinatorBackoffIncrease time.Duration,
	coordinatorMaxBackoff time.Duration,
) *LeafServer {
	return &LeafServer{
		scheduler:                       scheduler,
		database:                        httpClient,
		functionIdCache:                 make(map[string]kv.FunctionData),
		poolManager:                     *NewPoolManager(1, 100, 120*time.Second),
		coordinator:                     NewInstanceCoordinator(coordinatorBackoff, coordinatorBackoffIncrease, coordinatorMaxBackoff),
		maxStartingInstancesPerFunction: maxStartingInstancesPerFunction,
		startingInstanceWaitTimeout:     startingInstanceWaitTimeout,
		maxRunningInstancesPerFunction:  maxRunningInstancesPerFunction,
	}
}

func (s *LeafServer) callWorker(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, *CallMetadata, error) {
	pool, err := s.poolManager.GetPool(string(workerID), func() (*grpc.ClientConn, error) {
		conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
		}
		return conn, nil
	})
	if err != nil {
		log.Printf("[callWorker] Failed to get connection pool for worker %s: %v", workerID, err)
		return nil, nil, err
	}

	conn, err := pool.Get(ctx)
	if err != nil {
		log.Printf("Failed to get connection from pool for worker %s: %v", workerID, err)
		return nil, nil, err
	}

	var resp *common.CallResponse
	var trailer metadata.MD
	err = func() error {
		defer conn.Close() // Returns connection to pool
		client := controllerPB.NewControllerClient(conn)

		callReq := &common.CallRequest{
			InstanceId: &common.InstanceID{Id: string(instanceID)},
			FunctionId: &common.FunctionID{Id: string(functionID)},
			Data:       req.Data,
		}

		var err error
		// Extract timestamps from the trailer

		resp, err = client.Call(ctx, callReq, grpc.Trailer(&trailer))
		if err != nil {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.Unavailable {
				return &WorkerDownError{WorkerID: workerID, err: err}
			}
			return err
		}
		return nil
	}()

	if err != nil {
		return nil, nil, err
	}

	callMetadata := &CallMetadata{
		CallQueuedTimestamp:    trailer.Get("callQueuedTimestamp")[0],
		GotResponseTimestamp:   trailer.Get("gotResponseTimestamp")[0],
		FunctionProcessingTime: trailer.Get("functionProcessingTime")[0],
	}

	return &leaf.ScheduleCallResponse{Data: resp.Data, Error: resp.Error}, callMetadata, nil
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

	pool, err := s.poolManager.GetPool(string(workerID), factory)
	if err != nil {
		return "", err
	}
	conn, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}

	defer conn.Close()
	client := controllerPB.NewControllerClient(conn)

	instanceID, err := client.Start(ctx, &common.FunctionID{Id: string(functionId)})
	if err != nil {
		return "", err
	}

	return state.InstanceID(instanceID.Id), nil
}

type PoolManager struct {
	mu          sync.RWMutex
	pools       map[string]*grpcpool.Pool
	minConns    int
	maxConns    int
	idleTimeout time.Duration
}

// NewPoolManager creates a new PoolManager with configurable settings
func NewPoolManager(minConns, maxConns int, idleTimeout time.Duration) *PoolManager {
	return &PoolManager{
		pools:       make(map[string]*grpcpool.Pool),
		minConns:    minConns,
		maxConns:    maxConns,
		idleTimeout: idleTimeout,
	}
}

// GetPool returns the pool for a given worker, creating one if it doesn't exist
func (pm *PoolManager) GetPool(workerID string, factory grpcpool.Factory) (*grpcpool.Pool, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pool, ok := pm.pools[workerID]; ok {
		return pool, nil
	}

	// Create a new pool with configured settings
	pool, err := grpcpool.New(factory, pm.minConns, pm.maxConns, pm.idleTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool for worker %s: %w", workerID, err)
	}

	log.Printf("[PoolManager] Created new connection pool for worker %s", workerID)
	pm.pools[workerID] = pool
	return pool, nil
}

// RemovePool removes a pool for a worker that is no longer active
func (pm *PoolManager) RemovePool(workerID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pool, ok := pm.pools[workerID]; ok {
		pool.Close()
		delete(pm.pools, workerID)
		log.Printf("[PoolManager] Removed connection pool for worker %s", workerID)
	}
}

// Cleanup closes all pools
func (pm *PoolManager) Cleanup() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for workerID, pool := range pm.pools {
		pool.Close()
		delete(pm.pools, workerID)
		log.Printf("[PoolManager] Cleaned up connection pool for worker %s", workerID)
	}
}
