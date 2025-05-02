package api

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	"google.golang.org/grpc/status"
)

type LeafServer struct {
	leaf.UnimplementedLeafServer
	scheduler       scheduling.Scheduler
	database        kv.FunctionMetadataStore
	functionIdCache map[string]kv.FunctionData
	poolManager     PoolManager
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

func (s *LeafServer) ScheduleCall(ctx context.Context, req *leaf.ScheduleCallRequest) (*leaf.ScheduleCallResponse, error) {

	//Check if the functionID is cached, if not get it from database
	if _, ok := s.functionIdCache[req.FunctionID.Id]; !ok {
		ImageTag, Config, err := s.database.Get(req.FunctionID)
		if err != nil {
			nke := &kv.NoSuchKeyError{}
			if errors.As(err, &nke) {
				return nil, status.Errorf(codes.NotFound, "failed to get function from database: %s", req.FunctionID.Id)
			}
			return nil, fmt.Errorf("failed to get function from database: %w", err)
		}

		s.functionIdCache[req.FunctionID.Id] = kv.FunctionData{
			Config:   Config,
			ImageTag: ImageTag,
		}
	}

	//data := s.functionIdCache[req.FunctionID.Id]
	functionId := state.FunctionID(req.FunctionID.Id)

	workerID, instanceID, err := s.scheduler.Schedule(ctx, functionId)
	if err != nil {
		return nil, err
	}

	if instanceID == "" {
		// There is no idle instance available
		instanceID, err = s.startInstance(ctx, workerID, functionId)
		if err != nil {
			return nil, err
		}
		log.Printf("Started new instance for function %s on worker %s, instanceID: %s", functionId, workerID, instanceID)
		s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateNew)
	} else {
		// An Idle instance was found
		s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateRunning)
	}

	resp, err := s.callWorker(ctx, workerID, functionId, instanceID, req)
	if err != nil {
		switch err.(type) {
		case *controller.ContainerCrashError, *controller.InstanceNotFoundError:
			s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateDown)
			// TODO: handle this and dont return.
			return nil, err
		case *WorkerDownError:
			s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateDown)
			s.scheduler.UpdateWorkerState(workerID, state.WorkerStateDown)
			return nil, err
		default:
			return nil, err
		}
	}
	log.Printf("Recieved response from worker %s, instanceID: %s, response: %v", workerID, instanceID, resp)
	// The instance is no longer running
	s.scheduler.UpdateInstanceState(workerID, functionId, instanceID, state.InstanceStateIdle)

	return resp, nil
}

func NewLeafServer(scheduler scheduling.Scheduler, httpClient kv.FunctionMetadataStore) *LeafServer {
	return &LeafServer{
		scheduler:       scheduler,
		database:        httpClient,
		functionIdCache: make(map[string]kv.FunctionData),
		poolManager:     *NewPoolManager(),
	}
}

// TODO: refactor this to use a pool of connections.
// https://promisefemi.vercel.app/blog/grpc-client-connection-pooling
// https://github.com/processout/grpc-go-pool/blob/master/pool.go
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
	defer conn.Close()
	client := controllerPB.NewControllerClient(conn)

	callReq := &common.CallRequest{
		InstanceId: &common.InstanceID{Id: string(instanceID)},
		FunctionId: &common.FunctionID{Id: string(functionID)},
		Data:       req.Data,
	}

	resp, err := client.Call(ctx, callReq)
	if err != nil {
		// Check if it's a connection error
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unavailable {
			return nil, &WorkerDownError{WorkerID: workerID, err: err}
		}
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

	//TODO: function tag != image tag HERE WE NEED TO GET fid FROM HTTP

	instanceID, err := client.Start(ctx, &common.FunctionID{Id: string(functionId)})
	if err != nil {
		return "", err
	}

	return state.InstanceID(instanceID.Id), nil
}

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
