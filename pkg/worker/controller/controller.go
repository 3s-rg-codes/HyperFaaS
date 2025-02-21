package controller

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

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
)

type Controller struct {
	controller.UnimplementedControllerServer
	runtime      cr.ContainerRuntime
	CallerServer *caller.CallerServer
	StatsManager *stats.StatsManager
	logger       *slog.Logger
	address      string
}

func (s *Controller) Start(ctx context.Context, req *controller.StartRequest) (*common.InstanceID, error) {

	instanceId, err := s.runtime.Start(ctx, req.ImageTag.Tag, req.Config)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := instanceId
	if len(instanceId) > 12 {
		shortID = instanceId[:12]
		s.logger.Debug("Truncating ID", "id", instanceId, "shortID", shortID)
	}

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Container(shortID).Start().Failed())
		return nil, err

	}
	s.StatsManager.Enqueue(stats.Event().Container(shortID).Start().Success())

	s.CallerServer.RegisterFunctionInstance(shortID)

	return &common.InstanceID{Id: shortID}, nil
}

// This function passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	// Check if the instance ID is present in the FunctionCalls map
	if _, ok := s.CallerServer.FunctionCalls.FcMap[req.InstanceId.Id]; !ok {
		err := &InstanceNotFoundError{InstanceID: req.InstanceId.Id}
		s.logger.Error("Passing call with payload", "error", err.Error(), "instance ID", req.InstanceId.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if the instance ID is present in the FunctionResponses map
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
		// stats
		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Call().Success())

	}()

	select {

	case data := <-s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]:
		cancelCrash()
		s.StatsManager.Enqueue(stats.Event().Function(req.FunctionId.Id).Container(req.InstanceId.Id).Response().Success())
		s.logger.Debug("Extracted response", "response", data, "instance ID", req.InstanceId.Id)
		response := &common.CallResponse{Data: data}
		return response, nil

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
					InstanceId: data.InstanceID,
					FunctionId: data.FunctionID,
					Type:       controller.Type(data.Type),
					Event:      controller.Event(data.Event),
					Status:     controller.Status(data.Status),
				}); err != nil {
				s.logger.Error("Error streaming data", "error", err, "node_id", req.NodeID)
				return err
			}
			s.logger.Debug("Sent status update", "node_id", req.NodeID)
		} else {
			s.logger.Debug("Stream closed", "node_id", req.NodeID)
			// re buffer the data
			s.StatsManager.Enqueue(&data)
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

func NewController(runtime cr.ContainerRuntime, callerServer *caller.CallerServer, statsManager *stats.StatsManager, logger *slog.Logger, address string) *Controller {
	return &Controller{
		runtime:      runtime,
		StatsManager: statsManager,
		CallerServer: callerServer,
		logger:       logger,
		address:      address,
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
