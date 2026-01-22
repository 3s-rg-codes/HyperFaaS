package controller

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	sharedmetrics "github.com/3s-rg-codes/HyperFaaS/pkg/metrics"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	workerPB "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Controller struct {
	workerPB.UnimplementedWorkerServer
	runtime           cr.ContainerRuntime
	StatsManager      *stats.StatsManager
	metricsSampler    *stats.MetricsSampler
	metricsInterval   time.Duration
	resourceManager   *ResourceManager
	workerContainerID string
	logger            *slog.Logger
	address           string
	metadataClient    metadataProvider
	readySignals      *ReadySignals
}
type metadataProvider interface {
	GetFunction(ctx context.Context, id string) (*metadata.FunctionMetadata, error)
}

func (s *Controller) Start(ctx context.Context, req *workerPB.StartRequest) (*workerPB.StartResponse, error) {
	meta, err := s.metadataClient.GetFunction(ctx, req.FunctionId)
	if err != nil {
		if errors.Is(err, metadata.ErrFunctionNotFound) {
			s.logger.Warn("Function metadata not found", "functionID", req.FunctionId)
			return nil, status.Errorf(codes.NotFound, "function %s is not registered", req.FunctionId)
		}
		s.logger.Error("Failed to fetch function metadata", "functionID", req.FunctionId, "error", err)
		return nil, status.Errorf(codes.Unavailable, "metadata lookup failed: %v", err)
	}

	if meta.Image == nil || meta.Image.Tag == "" {
		s.logger.Error("Function metadata missing image tag", "functionID", req.FunctionId)
		return nil, status.Errorf(codes.InvalidArgument, "function %s has no image configured", req.FunctionId)
	}
	if meta.Config == nil {
		s.logger.Error("Function metadata missing config", "functionID", req.FunctionId)
		return nil, status.Errorf(codes.InvalidArgument, "function %s has no config configured", req.FunctionId)
	}

	s.logger.Debug("Starting container with params:", "tag", meta.Image, "memory", meta.Config.Memory, "quota", meta.Config.Cpu.Quota, "period", meta.Config.Cpu.Period)

	container, err := s.runtime.Start(ctx, req.FunctionId, meta.Image.Tag, meta.Config)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := container.Id
	if len(container.Id) > 12 {
		shortID = container.Id[:12]
	}
	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId).Container(shortID).Start().Failed())
		s.logger.Error("Failed to start container", "error", err)
		return nil, err
	}

	s.readySignals.AddInstance(shortID)
	if s.resourceManager != nil {
		s.resourceManager.AddContainer(container.Id)
	}
	// Container has been requested; we actually dont know if its running or not
	s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId).Container(shortID).Start().Success())

	go s.monitorContainerLifecycle(req.FunctionId, container)

	s.logger.Debug("Created container", "functionID", req.FunctionId, "instanceID", shortID, "instanceIP", container.InternalIP)

	// Block until the container is ready to serve requests
	s.readySignals.WaitReady(shortID)

	return &workerPB.StartResponse{
		InstanceId:         shortID,
		InstanceInternalIp: container.InternalIP,
		InstanceExternalIp: container.ExternalIP,
		InstanceName:       container.Name,
	}, nil
}

func (s *Controller) SignalReady(ctx context.Context, req *workerPB.SignalReadyRequest) (*emptypb.Empty, error) {
	s.readySignals.SignalReady(req.InstanceId)
	return &emptypb.Empty{}, nil
}

func (s *Controller) Stop(ctx context.Context, req *workerPB.StopRequest) (*workerPB.StopResponse, error) {
	if s.resourceManager != nil {
		s.resourceManager.RemoveContainer(req.InstanceId)
	}
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
	metrics, err := s.getMetricsSnapshot()
	if err != nil {
		s.logger.Error("failed to sample metrics", "error", err)
		return nil, status.Errorf(codes.Unavailable, "failed to sample metrics: %v", err)
	}

	return metricsToProto(metrics), nil
}

func (s *Controller) MetricsStream(req *workerPB.MetricsRequest, stream grpc.ServerStreamingServer[workerPB.MetricsUpdate]) error {
	ctx := stream.Context()
	interval := s.metricsInterval
	if interval <= 0 {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sendLatest := func() error {
		metrics, err := s.getMetricsSnapshot()
		if err != nil {
			return status.Error(codes.Unavailable, "metrics unavailable")
		}
		return stream.Send(metricsToProto(metrics))
	}

	if err := sendLatest(); err != nil {
		s.logger.Error("error streaming metrics", "error", err, "node_id", req.NodeId)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := sendLatest(); err != nil {
				s.logger.Error("error streaming metrics", "error", err, "node_id", req.NodeId)
				return err
			}
		}
	}
}

func NewController(runtime cr.ContainerRuntime,
	statsManager *stats.StatsManager,
	logger *slog.Logger,
	address string,
	metadataClient metadataProvider,
	readySignals *ReadySignals,
	containerized bool,
	metricsInterval time.Duration,
	workerBudgetCPU float64,
	workerBudgetMemory uint64,
) *Controller {
	metricsSampler := stats.NewMetricsSampler(containerized, logger.With("component", "metrics_sampler"))
	var resourceManager *ResourceManager
	var workerContainerID string
	if containerized {
		resourceManager = NewResourceManager(runtime, logger, metricsInterval, workerBudgetCPU, workerBudgetMemory)
		if hostname, err := os.Hostname(); err == nil {
			workerContainerID = hostname
		} else {
			logger.Warn("failed to read hostname for worker container", "error", err)
		}
	}
	return &Controller{
		runtime:           runtime,
		StatsManager:      statsManager,
		metricsSampler:    metricsSampler,
		metricsInterval:   metricsInterval,
		resourceManager:   resourceManager,
		workerContainerID: workerContainerID,
		logger:            logger,
		address:           address,
		metadataClient:    metadataClient,
		readySignals:      readySignals,
	}
}

func (s *Controller) StartServer(ctx context.Context) {
	grpcServer := grpc.NewServer(
	// Uncomment if you need logging of all grpc requests and responses.
	// grpc.ChainUnaryInterceptor(utils.InterceptorLogger(s.logger)),
	)
	// TODO pass context to sub servers
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// Start the stats manager
	if s.resourceManager != nil {
		s.resourceManager.SetContext(ctx)
		if s.workerContainerID != "" {
			s.resourceManager.AddContainer(s.workerContainerID)
		}
		go s.resourceManager.Run(ctx)
	} else {
		go s.metricsSampler.Run(ctx, s.metricsInterval)
	}
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

// Monitors the container lifecycle and handles exit, crash, and oom.
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
	case cr.ContainerEventExit:
		s.logger.Debug("Container exited", "instanceID", c.Id)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.Id).Stop().Success())
	case cr.ContainerEventOOM:
		s.logger.Debug("Container ran out of memory", "instanceID", c.Id)
		s.StatsManager.Enqueue(stats.Event().Function(functionID).Container(c.Id).Down().Failed())
	default:
		s.logger.Debug("Unexpected container event", "instanceID", c.Id, "event", event)
	}
	if s.resourceManager != nil {
		s.resourceManager.RemoveContainer(c.Id)
	}
}

func (s *Controller) getMetricsSnapshot() (sharedmetrics.ResourceMetrics, error) {
	if s.resourceManager != nil {
		metrics, ok := s.resourceManager.Latest()
		if !ok {
			return sharedmetrics.ResourceMetrics{}, errors.New("metrics unavailable")
		}
		return metrics, nil
	}
	metrics, ok := s.metricsSampler.Latest()
	if ok {
		return metrics, nil
	}
	metrics, err := s.metricsSampler.Sample()
	if err != nil {
		return sharedmetrics.ResourceMetrics{}, err
	}
	return metrics, nil
}

func metricsToProto(metrics sharedmetrics.ResourceMetrics) *workerPB.MetricsUpdate {
	return &workerPB.MetricsUpdate{
		CpuUtilizationRaw:        metrics.CPUUtilizationRaw,
		CpuUtilizationPercent:    metrics.CPUUtilizationPercent,
		MemoryUtilizationRaw:     metrics.MemoryUtilizationRaw,
		MemoryUtilizationPercent: metrics.MemoryUtilizationPercent,
	}
}
