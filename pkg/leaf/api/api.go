package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerPB "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	grpcpool "github.com/processout/grpc-go-pool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Here we could have a cache of functions and if they are scale 0 or 1.

type LeafServer struct {
	leaf.UnimplementedLeafServer
	state                    *state.SmallState
	leafConfig               config.LeafConfig
	workerIds                []state.WorkerID
	functionMetricChans      map[state.FunctionID]chan bool
	functionMetricChansMutex sync.RWMutex
	database                 kv.FunctionMetadataStore
	functionIdCache          map[string]kv.FunctionData
	poolManager              PoolManager
	logger                   *slog.Logger
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

	s.functionMetricChansMutex.Lock()
	s.functionMetricChans[state.FunctionID(functionID.Id)] = make(chan bool, 10000)
	s.functionMetricChansMutex.Unlock()

	s.state.AddFunction(state.FunctionID(functionID.Id),
		s.functionMetricChans[state.FunctionID(functionID.Id)],
		func(ctx context.Context, functionID state.FunctionID, workerID state.WorkerID) error {
			_, err := s.startInstance(ctx, workerID, functionID)
			if err != nil {
				return err
			}
			return nil
		})

	return &leaf.CreateFunctionResponse{
		FunctionID: functionID,
	}, nil

}

func (s *LeafServer) ScheduleCall(ctx context.Context, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {
	autoscaler := s.state.GetAutoscaler(state.FunctionID(req.FunctionID.Id))
	if autoscaler.IsScaledDown() {
		err := autoscaler.ForceScaleUp(ctx)
		if err != nil {
			if errors.As(err, &state.TooManyStartingInstancesError{}) {
				time.Sleep(s.leafConfig.PanicBackoff)
				s.leafConfig.PanicBackoff = s.leafConfig.PanicBackoff + s.leafConfig.PanicBackoffIncrease
				if s.leafConfig.PanicBackoff > s.leafConfig.PanicMaxBackoff {
					s.leafConfig.PanicBackoff = s.leafConfig.PanicMaxBackoff
				}
				return s.ScheduleCall(ctx, req)
			}
			if errors.As(err, &state.ScaleUpFailedError{}) {
				//TODO improve error message with better info.
				return nil, status.Errorf(codes.ResourceExhausted, "failed to scale up function")
			}
		}
	}
	if autoscaler.IsPanicMode() {
		time.Sleep(s.leafConfig.PanicBackoff)
		s.leafConfig.PanicBackoff = s.leafConfig.PanicBackoff + s.leafConfig.PanicBackoffIncrease
		if s.leafConfig.PanicBackoff > s.leafConfig.PanicMaxBackoff {
			s.leafConfig.PanicBackoff = s.leafConfig.PanicMaxBackoff
		}
		return s.ScheduleCall(ctx, req)
	}

	// TODO: pick a better way to pick a worker.
	randWorker := s.workerIds[rand.Intn(len(s.workerIds))]

	s.functionMetricChansMutex.RLock()
	metricChan := s.functionMetricChans[state.FunctionID(req.FunctionID.Id)]
	s.functionMetricChansMutex.RUnlock()
	metricChan <- true
	// Note: we send function id as instance id because I havent updated the proto yet. But the call instance endpoint is now call function. worker handles the instance id.
	resp, err := s.callWorker(ctx, randWorker, state.FunctionID(req.FunctionID.Id), state.InstanceID(req.FunctionID.Id), req)
	if err != nil {
		return nil, err
	}
	metricChan <- false

	return resp, nil
}

func NewLeafServer(
	leafConfig config.LeafConfig,
	httpClient kv.FunctionMetadataStore,
	workerIds []state.WorkerID,
	logger *slog.Logger,
) *LeafServer {
	ls := LeafServer{
		database:            httpClient,
		functionIdCache:     make(map[string]kv.FunctionData),
		functionMetricChans: make(map[state.FunctionID]chan bool),
		poolManager:         *NewPoolManager(1, 100, 120*time.Second),
		workerIds:           workerIds,
		state:               state.NewSmallState(workerIds, logger),
		logger:              logger,
		leafConfig:          leafConfig,
	}
	ls.state.RunReconciler(context.Background())
	return &ls
}

func (s *LeafServer) callWorker(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {
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

	conn, err := pool.Get(ctx)
	if err != nil {
		log.Printf("Failed to get connection from pool for worker %s: %v", workerID, err)
		return nil, err
	}

	var resp *common.CallResponse
	err = func() error {
		defer conn.Close() // Returns connection to pool
		client := controllerPB.NewControllerClient(conn)

		callReq := &common.CallRequest{
			InstanceId: &common.InstanceID{Id: string(instanceID)},
			FunctionId: &common.FunctionID{Id: string(functionID)},
			Data:       req.Data,
		}

		var err error
		resp, err = client.Call(ctx, callReq)
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
		return nil, err
	}

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

	resp, err := client.Start(ctx, &common.FunctionID{Id: string(functionId)})
	if err != nil {
		return "", err
	}

	return state.InstanceID(resp.InstanceId.Id), nil
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
