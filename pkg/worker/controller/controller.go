package controller

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerPB "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	cpu "github.com/shirou/gopsutil/v4/cpu"
	mem "github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Controller struct {
	workerPB.UnimplementedWorkerServer
	runtime         cr.ContainerRuntime
	StatsManager    *stats.StatsManager
	callRouter      CallRouter
	logger          *slog.Logger
	address         string
	dbClient        kv.FunctionMetadataStore
	functionIDCache map[string]kv.FunctionData
	readySignals    *ReadySignals
	mu              sync.RWMutex
}

type CallRouter interface {
	AddInstance(functionID string, ip string)
	CallFunction(ctx context.Context, functionID string, req *common.CallRequest) (*common.CallResponse, error)
	HandleInstanceTimeout(functionID string, ip string)
}

func (s *Controller) Start(ctx context.Context, req *workerPB.StartRequest) (*workerPB.StartResponse, error) {
	// Check if we have config and image for ID cached and if not get it from db
	s.mu.RLock()
	_, ok := s.functionIDCache[req.FunctionId]
	s.mu.RUnlock()

	if !ok {
		s.logger.Debug("FunctionData not available locally, fetching from server", "functionID", req.FunctionId)
		imageTag, config, err := s.dbClient.Get(req.FunctionId)
		if err != nil {
			return &workerPB.StartResponse{}, err
		}

		d := kv.FunctionData{
			Config: config,
			Image:  &common.Image{Tag: imageTag},
		}

		s.mu.Lock()
		s.functionIDCache[req.FunctionId] = d
		s.mu.Unlock()
	}

	functionData := s.functionIDCache[req.FunctionId]
	s.logger.Debug("Starting container with params:", "tag", functionData.Image, "memory", functionData.Config.Memory, "quota", functionData.Config.Cpu.Quota, "period", functionData.Config.Cpu.Period)

	container, err := s.runtime.Start(ctx, req.FunctionId, functionData.Image.Tag, functionData.Config)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := container.Id
	if len(container.Id) > 12 {
		shortID = container.Id[:12]
	}
	// Add the instance to the map to wait for the ready signal
	s.readySignals.AddInstance(shortID)

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId).Container(shortID).Start().Failed())
		s.logger.Error("Failed to start container", "error", err)
		return nil, err
	}
	// Container has been requested; we actually dont know if its running or not
	s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId).Container(shortID).Start().Success())

	go s.monitorContainerLifecycle(req.FunctionId, container)

	// We use the functionID as the key for the call router
	s.callRouter.AddInstance(req.FunctionId, container.IP)

	s.logger.Debug("Created container", "functionID", req.FunctionId, "instanceID", shortID, "instanceIP", container.IP)

	// Block until the container is ready to serve requests
	s.readySignals.WaitReady(shortID)

	return &workerPB.StartResponse{
		InstanceId:   shortID,
		InstanceIp:   container.IP,
		InstanceName: container.Name,
	}, nil
}

func (s *Controller) SignalReady(ctx context.Context, req *workerPB.SignalReadyRequest) (*emptypb.Empty, error) {
	s.readySignals.SignalReady(req.InstanceId)
	return &emptypb.Empty{}, nil
}

func (s *Controller) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	s.logger.Debug("Calling function", "functionID", req.FunctionId)
	return s.callRouter.CallFunction(ctx, req.FunctionId, req)
}

func (s *Controller) Stop(ctx context.Context, req *workerPB.StopRequest) (*workerPB.StopResponse, error) {
	err := s.runtime.Stop(ctx, req.InstanceId)
	if err != nil {
		s.logger.Error("Failed to stop container", "instance ID", req.InstanceId, "error", err)
		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId).Stop().Failed())
		return nil, err
	}

	s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId).Stop().Success())

	s.logger.Debug("Successfully enqueued event for container", "container", req.InstanceId)

	return &workerPB.StopResponse{InstanceId: req.InstanceId}, nil
}

// Streams the status updates to a client.
// Using a channel to listen to the stats manager for status updates
// Status Updates are defined in pkg/stats/statusUpdate.go
func (s *Controller) Status(req *workerPB.StatusRequest, stream workerPB.Worker_StatusServer) error {
	ctx := stream.Context()
	nodeID := req.NodeId

	// Get or create listener channel
	statsChannel := s.StatsManager.GetListenerByID(nodeID)
	if statsChannel == nil {
		// Create a new channel if none exists
		statsChannel = make(chan stats.StatusUpdate, 10000)
		s.StatsManager.AddListener(nodeID, statsChannel)
	}

	// Handle channel receives and context cancellation
	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("Stream context done", "node_id", nodeID, "error", ctx.Err())
			s.StatsManager.RemoveListener(nodeID)
			return ctx.Err()

		case data, ok := <-statsChannel:
			if !ok {
				// Channel was closed
				s.logger.Debug("Stats channel closed", "node_id", nodeID)
				return nil
			}

			if err := stream.Send(
				&workerPB.StatusUpdate{
					InstanceId: data.InstanceID,
					FunctionId: data.FunctionID,
					Timestamp:  timestamppb.New(data.Timestamp),
					Type:       workerPB.VirtualizationType(data.Type),
					Event:      workerPB.Event(data.Event),
					Status:     workerPB.Status(data.Status),
				}); err != nil {
				s.logger.Error("Error streaming data", "error", err, "node_id", nodeID)
				s.StatsManager.RemoveListener(nodeID)
				return err
			}
			s.logger.Debug("Sent status update", "node_id", nodeID, "event", data.Event, "status", data.Status)
		}
	}
}

func (s *Controller) Metrics(ctx context.Context, req *workerPB.MetricsRequest) (*workerPB.MetricsUpdate, error) {
	cpu_percentage_percpu, err1 := cpu.Percent(time.Millisecond*10, true)
	virtual_mem, err2 := mem.VirtualMemory()

	if err1 != nil || err2 != nil {
		return nil, err1
	}
	return &workerPB.MetricsUpdate{CpuPercentPercpus: cpu_percentage_percpu, UsedRamPercent: virtual_mem.UsedPercent}, nil
}

func NewController(runtime cr.ContainerRuntime,
	statsManager *stats.StatsManager,
	logger *slog.Logger,
	address string,
	client kv.FunctionMetadataStore,
	callRouter CallRouter,
	readySignals *ReadySignals,
) *Controller {
	return &Controller{
		runtime:         runtime,
		StatsManager:    statsManager,
		logger:          logger,
		address:         address,
		callRouter:      callRouter,
		dbClient:        client,
		functionIDCache: make(map[string]kv.FunctionData),
		readySignals:    readySignals,
	}
}

func (s *Controller) StartServer(ctx context.Context) {
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(utils.InterceptorLogger(s.logger)),
	)
	// TODO pass context to sub servers
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// Start the stats manager

	go func() {
		s.StatsManager.StartStreamingToListeners(ctx)
	}()

	healthcheck := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthcheck)
	healthcheck.SetServingStatus("worker", healthpb.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		s.logger.Error("failed to listen", "error", err)
	}

	workerPB.RegisterWorkerServer(grpcServer, s)

	s.logger.Debug("Controller Server listening on", "address", lis.Addr())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		return
	case <-sigCh:
		s.logger.Info("Shutting down gracefully...")

		grpcServer.GracefulStop()
	default:
		if err := grpcServer.Serve(lis); err != nil {
			s.logger.Error("Controller Server failed to serve", "error", err)
		}
	}
}

// Monitors the container lifecycle and handles the possible scenarios (timeout, crash, oom)
func (s *Controller) monitorContainerLifecycle(functionID string, c cr.Container) {
	s.logger.Debug("Starting container monitoring", "instanceID", c.Id, "functionID", functionID)

	// Use a background context so monitoring continues even after the original request context expires
	monitorCtx := context.Background()

	event, err := s.runtime.MonitorContainer(monitorCtx, c.Id, functionID)
	if err != nil {
		s.logger.Error("Failed to monitor container", "instanceID", c.Id, "error", err)
		return
	}

	switch event {
	case cr.ContainerEventCrash:
		s.logger.Debug("Container crashed", "instanceID", c.Id, "error", err)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.Id).Down().Failed())
		s.callRouter.HandleInstanceTimeout(functionID, c.IP)
	case cr.ContainerEventTimeout, cr.ContainerEventExit:
		s.logger.Debug("Container timed out gracefully", "instanceID", c.Id)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.Id).Timeout().Success())
		s.callRouter.HandleInstanceTimeout(functionID, c.IP)
	case cr.ContainerEventOOM:
		s.logger.Debug("Container ran out of memory", "instanceID", c.Id)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.Id).Down().Failed())
		s.callRouter.HandleInstanceTimeout(functionID, c.IP)
	default:
		s.logger.Debug("Unexpected container event", "instanceID", c.Id, "event", event)
	}
}
