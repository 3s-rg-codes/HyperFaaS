package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerPB "github.com/3s-rg-codes/HyperFaaS/proto/worker"
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
	functionIdCacheMutex     sync.RWMutex
	workerClients            workerClients
	logger                   *slog.Logger
}

type workerClients struct {
	mu      sync.RWMutex
	clients map[state.WorkerID]workerClient
}

type workerClient struct {
	conn   *grpc.ClientConn
	client workerPB.WorkerClient
}

// RegisterFunction creates local state for the function.
func (s *LeafServer) RegisterFunction(ctx context.Context, req *leaf.RegisterFunctionRequest) (*common.CreateFunctionResponse, error) {

	s.functionIdCacheMutex.Lock()
	s.functionIdCache[req.FunctionId] = kv.FunctionData{
		Config: req.Config.Config,
		Image:  req.Config.Image,
	}
	s.functionIdCacheMutex.Unlock()

	functionID := state.FunctionID(req.FunctionId)

	s.functionMetricChansMutex.Lock()
	if _, exists := s.functionMetricChans[functionID]; !exists {
		s.functionMetricChans[functionID] = make(chan bool, 10000)
	}
	s.functionMetricChansMutex.Unlock()

	s.state.AddAutoscaler(functionID,
		s.functionMetricChans[functionID],
		func(ctx context.Context, functionID state.FunctionID, workerID state.WorkerID) error {
			_, err := s.startInstance(ctx, workerID, functionID)
			return err
		})

	return &common.CreateFunctionResponse{FunctionId: string(functionID)}, nil
}

func (s *LeafServer) ScheduleCall(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	autoscaler, ok := s.state.GetAutoscaler(state.FunctionID(req.FunctionId))
	if !ok {
		return nil, status.Errorf(codes.NotFound, "leaf could not schedule: function id %s not found", req.FunctionId)
	}

	var targetWorker state.WorkerID
	var err error
	currentBackoff := s.leafConfig.PanicBackoff
	// check if we are scaled down or in panic mode.
	// this loop helps in case of many concurrent calls
	for {
		if autoscaler.IsScaledDown() {
			targetWorker, err = autoscaler.ForceScaleUp(ctx)
			if err != nil {
				var tooManyStartingInstancesError *state.TooManyStartingInstancesError
				var scaleUpFailedError *state.ScaleUpFailedError
				if errors.As(err, &tooManyStartingInstancesError) {
					time.Sleep(currentBackoff)
					currentBackoff = currentBackoff + s.leafConfig.PanicBackoffIncrease
					if currentBackoff > s.leafConfig.PanicMaxBackoff {
						currentBackoff = s.leafConfig.PanicMaxBackoff
					}
					continue
				}
				if errors.As(err, &scaleUpFailedError) {
					return nil, status.Errorf(codes.ResourceExhausted, "leaf could not schedule: failed to scale up function")
				}
			}
			// We have successfully scaled up
			break
		} else if autoscaler.IsPanicMode() {
			time.Sleep(currentBackoff)
			currentBackoff = currentBackoff + s.leafConfig.PanicBackoffIncrease
			if currentBackoff > s.leafConfig.PanicMaxBackoff {
				currentBackoff = s.leafConfig.PanicMaxBackoff
			}
			continue
		} else {
			break
		}
	}

	if targetWorker == "" {
		// pick a worker, we are not in panic mode and we are not scaled down
		targetWorker = autoscaler.Pick()
		if targetWorker == "" {
			return nil, status.Errorf(codes.NotFound, "leaf could not schedule: no worker currently hosting an instance for function %s", req.FunctionId)
		}
	}

	s.functionMetricChansMutex.RLock()
	metricChan := s.functionMetricChans[state.FunctionID(req.FunctionId)]
	s.functionMetricChansMutex.RUnlock()
	metricChan <- true
	defer func() { metricChan <- false }()

	resp, err := s.callWorker(ctx, targetWorker, state.FunctionID(req.FunctionId), req)
	if err != nil {
		return nil, err
	}

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
		workerClients:       workerClients{clients: make(map[state.WorkerID]workerClient)},
		workerIds:           workerIds,
		state:               state.NewSmallState(workerIds, logger),
		logger:              logger,
		leafConfig:          leafConfig,
	}
	ls.state.RunReconciler(context.Background())
	return &ls
}

func (s *LeafServer) callWorker(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID, req *common.CallRequest) (*common.CallResponse, error) {
	client, err := s.getOrCreateWorkerClient(workerID)
	if err != nil {
		return nil, err
	}

	var resp *common.CallResponse
	callReq := &common.CallRequest{
		FunctionId: string(functionID),
		Data:       req.Data,
	}

	resp, err = client.Call(ctx, callReq)
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.Unavailable {
			return nil, &WorkerDownError{WorkerID: workerID, err: err}
		}
		return nil, err
	}

	return resp, nil
}

func (s *LeafServer) startInstance(ctx context.Context, workerID state.WorkerID, functionId state.FunctionID) (state.InstanceID, error) {
	client, err := s.getOrCreateWorkerClient(workerID)
	if err != nil {
		return "", err
	}
	resp, err := client.Start(ctx, &workerPB.StartRequest{FunctionId: string(functionId)})
	if err != nil {
		return "", err
	}

	return state.InstanceID(resp.InstanceId), nil
}

func (s *LeafServer) getOrCreateWorkerClient(workerID state.WorkerID) (workerPB.WorkerClient, error) {
	s.workerClients.mu.RLock()
	client, ok := s.workerClients.clients[workerID]
	s.workerClients.mu.RUnlock()
	if !ok {
		c, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client: %w", err)
		}
		cl := workerPB.NewWorkerClient(c)
		s.workerClients.mu.Lock()
		s.workerClients.clients[workerID] = workerClient{
			conn:   c,
			client: cl,
		}
		s.workerClients.mu.Unlock()
		return cl, nil
	}
	return client.client, nil
}
