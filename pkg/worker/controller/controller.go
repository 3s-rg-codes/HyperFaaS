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
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/network"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	cpu "github.com/shirou/gopsutil/v4/cpu"
	mem "github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Controller struct {
	controller.UnimplementedControllerServer
	runtime         cr.ContainerRuntime
	StatsManager    *stats.StatsManager
	callRouter      *network.CallRouter
	logger          *slog.Logger
	address         string
	dbClient        kv.FunctionMetadataStore
	functionIDCache map[string]kv.FunctionData
	mu              sync.RWMutex
}

func (s *Controller) Start(ctx context.Context, req *common.FunctionID) (*controller.StartResponse, error) {

	//Check if we have config and image for ID cached and if not get it from db
	s.mu.RLock()
	_, ok := s.functionIDCache[req.Id]
	s.mu.RUnlock()

	if !ok {
		s.logger.Debug("FunctionData not available locally, fetching from server", "functionID", req.Id)
		imageTag, config, err := s.dbClient.Get(req)
		if err != nil {
			return &controller.StartResponse{}, err
		}

		d := kv.FunctionData{
			Config:   config,
			ImageTag: imageTag,
		}

		s.mu.Lock()
		s.functionIDCache[req.Id] = d
		s.mu.Unlock()
	}

	functionData := s.functionIDCache[req.Id]
	s.logger.Debug("Starting container with params:", "tag", functionData.ImageTag, "memory", functionData.Config.Memory, "quota", functionData.Config.Cpu.Quota, "period", functionData.Config.Cpu.Period)
	container, err := s.runtime.Start(ctx, req.Id, functionData.ImageTag.Tag, functionData.Config)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := container.InstanceID
	if len(container.InstanceID) > 12 {
		shortID = container.InstanceID[:12]
	}

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Function(req.Id).Container(shortID).Start().Failed())
		s.logger.Error("Failed to start container", "error", err)
		return nil, err
	}
	// Container has been requested; we actually dont know if its running or not
	s.StatsManager.Enqueue(stats.Event().Function(req.Id).Container(shortID).Start().Success())

	go s.monitorContainerLifecycle(req.Id, container)

	// We use the functionID as the key for the call router
	s.callRouter.AddInstance(req.Id, container.InstanceIP)

	s.logger.Debug("Created container", "functionID", req.Id, "instanceID", shortID, "instanceIP", container.InstanceIP)

	return &controller.StartResponse{InstanceId: &common.InstanceID{Id: shortID}, InstanceIp: container.InstanceIP, InstanceName: container.InstanceName}, nil
}

func (s *Controller) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	// TODO RENAME TO FUNCTIONID .
	s.logger.Debug("Calling function", "instanceID", req.InstanceId.Id, "functionID", req.FunctionId.Id)
	return s.callRouter.CallFunction(req.FunctionId.Id, req)
}

func (s *Controller) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) {

	resp, err := s.runtime.Stop(ctx, req)

	if err != nil {
		s.logger.Error("Failed to stop container", "instance ID", req.Id, "error", err)
		s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().Failed())
		return nil, err
	}

	s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().Success())

	s.logger.Debug("Successfully enqueued event for container", "container", req.Id)

	return resp, nil

}

// Streams the status updates to a client.
// Using a channel to listen to the stats manager for status updates
// Status Updates are defined in pkg/stats/statusUpdate.go
func (s *Controller) Status(req *controller.StatusRequest, stream controller.Controller_StatusServer) error {
	ctx := stream.Context()
	nodeID := req.NodeID

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
				&controller.StatusUpdate{
					InstanceId: &common.InstanceID{Id: data.InstanceID},
					FunctionId: &common.FunctionID{Id: data.FunctionID},
					Timestamp:  timestamppb.New(data.Timestamp),
					Type:       controller.VirtualizationType(data.Type),
					Event:      controller.Event(data.Event),
					Status:     controller.Status(data.Status),
				}); err != nil {
				s.logger.Error("Error streaming data", "error", err, "node_id", nodeID)
				s.StatsManager.RemoveListener(nodeID)
				return err
			}
			s.logger.Debug("Sent status update", "node_id", nodeID, "event", data.Event, "status", data.Status)
		}
	}
}

func (s *Controller) Metrics(ctx context.Context, req *controller.MetricsRequest) (*controller.MetricsUpdate, error) {

	cpu_percentage_percpu, err1 := cpu.Percent(time.Millisecond*10, true)
	virtual_mem, err2 := mem.VirtualMemory()

	if err1 != nil || err2 != nil {
		return nil, err1
	}
	return &controller.MetricsUpdate{CpuPercentPercpu: cpu_percentage_percpu, UsedRamPercent: virtual_mem.UsedPercent}, nil
}

func NewController(runtime cr.ContainerRuntime, statsManager *stats.StatsManager, logger *slog.Logger, address string, client kv.FunctionMetadataStore) *Controller {
	return &Controller{
		runtime:         runtime,
		StatsManager:    statsManager,
		logger:          logger,
		address:         address,
		callRouter:      network.NewCallRouter(logger),
		dbClient:        client,
		functionIDCache: make(map[string]kv.FunctionData),
	}
}

func (s *Controller) StartServer() {

	grpcServer := grpc.NewServer()
	// TODO pass context to sub servers
	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	//Start the stats manager

	go func() {
		s.StatsManager.StartStreamingToListeners()
	}()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		<-sigCh
		s.logger.Info("Shutting down gracefully...")

		grpcServer.GracefulStop()
	}()

	healthcheck := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthcheck)
	healthcheck.SetServingStatus("worker", healthpb.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", s.address)

	if err != nil {
		s.logger.Error("failed to listen", "error", err)
	}

	controller.RegisterControllerServer(grpcServer, s)

	s.logger.Debug("Controller Server listening on", "address", lis.Addr())

	if err := grpcServer.Serve(lis); err != nil {
		s.logger.Error("Controller Server failed to serve", "error", err)
	}

}

func (s *Controller) monitorContainerLifecycle(functionID string, c cr.Container) {
	s.logger.Debug("Starting container monitoring", "instanceID", c.InstanceID, "functionID", functionID)

	// Use a background context so monitoring continues even after the original request context expires
	monitorCtx := context.Background()

	event, err := s.runtime.MonitorContainer(monitorCtx, &common.InstanceID{Id: c.InstanceID}, functionID)

	if err != nil {
		s.logger.Error("Failed to monitor container", "instanceID", c.InstanceID, "error", err)
		return
	}

	switch event {
	case cr.ContainerEventCrash:
		s.logger.Debug("Container crashed", "instanceID", c.InstanceID, "error", err)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.InstanceID).Down().Failed())
		s.callRouter.HandleInstanceTimeout(functionID, c.InstanceIP)
	case cr.ContainerEventTimeout, cr.ContainerEventExit:
		s.logger.Debug("Container timed out gracefully", "instanceID", c.InstanceID)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.InstanceID).Timeout().Success())
		s.callRouter.HandleInstanceTimeout(functionID, c.InstanceIP)
	case cr.ContainerEventOOM:
		s.logger.Debug("Container ran out of memory", "instanceID", c.InstanceID)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.InstanceID).Down().Failed())
		s.callRouter.HandleInstanceTimeout(functionID, c.InstanceIP)
	default:
		s.logger.Debug("Unexpected container event", "instanceID", c.InstanceID, "event", event)
	}
}
