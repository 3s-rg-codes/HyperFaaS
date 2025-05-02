package controller

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	cpu "github.com/shirou/gopsutil/v4/cpu"
	mem "github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Controller struct {
	controller.UnimplementedControllerServer
	runtime         cr.ContainerRuntime
	CallerServer    *caller.CallerServer
	StatsManager    *stats.StatsManager
	logger          *slog.Logger
	address         string
	dbClient        kv.FunctionMetadataStore
	functionIDCache map[string]kv.FunctionData
}

func (s *Controller) Start(ctx context.Context, req *common.FunctionID) (*common.InstanceID, error) {

	//Check if we have config and image for ID cached and if not get it from db
	if _, ok := s.functionIDCache[req.Id]; !ok {
		s.logger.Debug("FunctionData not available locally, fetching from server", "functionID", req.Id)
		imageTag, config, err := s.dbClient.Get(req)
		if err != nil {
			return &common.InstanceID{}, err
		}

		d := kv.FunctionData{
			Config:   config,
			ImageTag: imageTag,
		}

		s.functionIDCache[req.Id] = d
	}

	functionData := s.functionIDCache[req.Id]

	s.logger.Debug("Starting container with params:", "tag", functionData.ImageTag, "memory", functionData.Config.Memory, "quota", functionData.Config.Cpu.Quota, "period", functionData.Config.Cpu.Period)
	instanceId, err := s.runtime.Start(ctx, req.Id, functionData.ImageTag.Tag, functionData.Config)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := instanceId
	if len(instanceId) > 12 {
		shortID = instanceId[:12]
		s.logger.Debug("Truncating ID", "id", instanceId, "shortID", shortID)
	}

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Function(req.Id).Container(shortID).Start().Failed())
		s.logger.Error("Failed to start container", "error", err)
		return nil, err
	}
	// Container has been requested; we actually dont know if its running or not
	s.StatsManager.Enqueue(stats.Event().Function(req.Id).Container(shortID).Start().Success())

	s.CallerServer.RegisterFunctionInstance(shortID)

	return &common.InstanceID{Id: shortID}, nil
}

// Call passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	if _, ok := s.CallerServer.FunctionCalls.FcMap[req.InstanceId.Id]; !ok {
		err := &InstanceNotFoundError{InstanceID: req.InstanceId.Id}
		s.logger.Error("Passing call with payload", "error", err.Error(), "instance ID", req.InstanceId.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	if _, ok := s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]; !ok {
		err := &InstanceNotFoundError{InstanceID: req.InstanceId.Id}
		s.logger.Error("Passing call with payload", "error", err.Error(), "instance ID", req.InstanceId.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if container crashes
	crashCtx, cancelCrash := context.WithCancel(ctx)
	defer cancelCrash()

	// Monitor for crashes in a goroutine
	crashChan := make(chan error, 1)
	go func() {
		if err := s.runtime.NotifyCrash(crashCtx, req.InstanceId); err != nil {
			crashChan <- err
		}
	}()

	s.logger.Debug("Passing call with payload", "payload", req.Data, "instance ID", req.InstanceId.Id)

	go func() {
		// Pass the call to the channel based on the instance ID
		s.CallerServer.QueueInstanceCall(req.InstanceId.Id, req.Data)
		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId.Id).Container(req.InstanceId.Id).Call().Success())

	}()

	responseChan := s.CallerServer.GetInstanceResponse(req.InstanceId.Id)

	select {

	case data := <-responseChan:
		cancelCrash()
		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId.Id).Container(req.InstanceId.Id).Response().Success())
		s.logger.Debug("Extracted response", "response", data, "instance ID", req.InstanceId.Id)
		return &common.CallResponse{Data: data}, nil

	case err := <-crashChan:

		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId.Id).Container(req.InstanceId.Id).Down())
		s.logger.Error("Container crashed while waiting for response", "instance ID", req.InstanceId.Id, "error", err)

		return nil, &ContainerCrashError{InstanceID: req.InstanceId.Id, ContainerError: err.Error()}

	}

}

func (s *Controller) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) {

	//unregister the function from the maps
	s.CallerServer.UnregisterFunctionInstance(req.Id)

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

	//If a node is re-hitting the status endpoint, use the existing channel
	statsChannel := s.StatsManager.GetListenerByID(req.NodeID)

	if statsChannel != nil {
		s.logger.Debug("Node is re-hitting the status endpoint", "node_id", req.NodeID)
	} else {
		statsChannel = make(chan stats.StatusUpdate, 10000)
		s.StatsManager.AddListener(req.NodeID, statsChannel)
	}
	for data := range statsChannel {
		// Check if the stream is closed
		if stream.Context().Err() == nil {
			if err := stream.Send(
				&controller.StatusUpdate{
					InstanceId: &common.InstanceID{Id: data.InstanceID},
					FunctionId: &common.FunctionID{Id: data.FunctionID},
					Timestamp:  timestamppb.New(data.Timestamp),
					Type:       controller.VirtualizationType(data.Type),
					Event:      controller.Event(data.Event),
					Status:     controller.Status(data.Status),
				}); err != nil {
				s.logger.Error("Error streaming data", "error", err, "node_id", req.NodeID)
				return err
			}
			s.logger.Debug("Sent status update", "node_id", req.NodeID, "event", data.Event, "status", data.Status)
		} else {
			s.logger.Debug("Stream closed", "node_id", req.NodeID)
			// re buffer the data
			statsChannel <- data
			s.StatsManager.RemoveListenerAfterTimeout(req.NodeID)
			return stream.Context().Err()
		}
	}

	return nil
}

func (s *Controller) Metrics(ctx context.Context, req *controller.MetricsRequest) (*controller.MetricsUpdate, error) {

	cpu_percentage_percpu, err1 := cpu.Percent(time.Millisecond*10, true)
	virtual_mem, err2 := mem.VirtualMemory()

	if err1 != nil || err2 != nil {
		return nil, err1
	}
	return &controller.MetricsUpdate{CpuPercentPercpu: cpu_percentage_percpu, UsedRamPercent: virtual_mem.UsedPercent}, nil
}

func NewController(runtime cr.ContainerRuntime, callerServer *caller.CallerServer, statsManager *stats.StatsManager, logger *slog.Logger, address string, client kv.FunctionMetadataStore) *Controller {
	return &Controller{
		runtime:         runtime,
		StatsManager:    statsManager,
		CallerServer:    callerServer,
		logger:          logger,
		address:         address,
		dbClient:        client,
		functionIDCache: make(map[string]kv.FunctionData),
	}
}

func (s *Controller) StartServer() {

	grpcServer := grpc.NewServer()
	// TODO pass context to sub servers
	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	//Start the caller server
	go func() {
		s.CallerServer.Start()
	}()

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
