package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net"
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
	"google.golang.org/grpc/status"
)

type Controller struct {
	controller.UnimplementedControllerServer
	runtime                  cr.ContainerRuntime
	CallerServer             *caller.CallerServer
	StatsManager             *stats.StatsManager
	logger                   *slog.Logger
	address                  string
	timeOutRemovingListeners time.Duration
}

func (s *Controller) Start(ctx context.Context, req *controller.StartRequest) (*common.InstanceID, error) {

	instanceId, err := s.runtime.Start(ctx, req.ImageTag.Tag, req.Config)

	s.logger.Debug("Started container", "ID", instanceId)

	// Truncate the ID to the first 12 characters to match Docker's short ID format
	shortID := instanceId
	if len(instanceId) > 12 {
		shortID = instanceId[:12]
		s.logger.Debug("Truncating ID", "id", instanceId, "shortID", shortID)
	}

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Container(shortID).Start().WithStatus("failed"))
		return nil, err
	}

	s.StatsManager.Enqueue(stats.Event().Container(shortID).Start().WithStatus("success"))

	s.logger.Debug("Registering Function", "id", shortID)
	s.CallerServer.RegisterFunction(shortID)

	return &common.InstanceID{Id: shortID}, nil
}

// This function passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	// Check if the instance ID is present in the FunctionCalls map
	if _, ok := s.CallerServer.FunctionCalls.FcMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID  does not exist")
		s.logger.Error("Passing call with payload", "error", err, "instance ID", req.InstanceId.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if the instance ID is present in the FunctionResponses map
	if _, ok := s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		s.logger.Error("Passing call with payload", "error", err, "instance ID", req.InstanceId.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if container crashes
	containerCrashed := make(chan error)
	//defer close(containerCrashed)

	go func() {
		containerCrashed <- s.runtime.NotifyCrash(ctx, req.InstanceId)
	}()

	s.logger.Debug("Passing call with payload", "payload", req.Data, "instance ID", req.InstanceId.Id)

	go func() {
		// Pass the call to the channel based on the instance ID
		s.CallerServer.PassCallToChannel(req.InstanceId.Id, req.Data)

		s.logger.Debug("Passed call to channel", "instaceID", req.InstanceId.Id)
		// stats
		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Call().WithStatus("success"))

	}()

	select {

	case resp := <-s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]:

		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Response().WithStatus("success"))

		s.logger.Debug("Extracted response", "response", string(resp.Data), "instance ID", req.InstanceId.Id)
		response := &common.CallResponse{Data: resp.Data, Error: &common.Error{Message: resp.Error}}
		return response, nil //TODO for some reason the bytes are base64 encoded

	case err := <-containerCrashed:

		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Die())

		s.logger.Error("Container crashed while waiting for response", "instance ID", req.InstanceId.Id, "error", err)

		return nil, fmt.Errorf("container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

	}

}

func (s *Controller) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) {

	//unregister the function from the maps
	s.CallerServer.UnregisterFunction(req.Id)

	resp, err := s.runtime.Stop(ctx, req)

	if err != nil {
		s.logger.Error("Failed to stop container", "instance ID", req.Id, "error", err)
		s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("failed"))
		return nil, err
	}

	s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("success"))

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
					Type:       data.Type,
					Event:      data.Event,
					Status:     data.Status,
				}); err != nil {
				s.logger.Error("Error streaming data", "error", err, "node_id", req.NodeID)
				s.StatsManager.RemoveListener(req.NodeID)
				return err
			}
			s.logger.Debug("Sent status update", "node_id", req.NodeID, "update", data)
		} else {
			s.logger.Debug("Stream closed", "node_id", req.NodeID)
			go s.StatsManager.RemoveListenerAfter(req.NodeID, s.timeOutRemovingListeners)
			// re buffer the data
			statsChannel <- data
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

func NewController(runtime cr.ContainerRuntime, caller *caller.CallerServer, logger *slog.Logger, address string, timeout time.Duration) *Controller {
	return &Controller{
		runtime:                  runtime,
		CallerServer:             caller,
		StatsManager:             stats.NewStatsManager(logger),
		logger:                   logger,
		address:                  address,
		timeOutRemovingListeners: timeout,
	}
}

func (s *Controller) StartServer() {

	//Start the caller server
	go func() {
		s.CallerServer.Start()
	}()

	//Start the stats manager

	go func() {
		s.StatsManager.StartStreamingToListeners()
	}()

	//Start the controller server
	grpcServer := grpc.NewServer()

	lis, err := net.Listen("tcp", ":50051")

	if err != nil {
		s.logger.Error("failed to listen", "error", err)
	}

	controller.RegisterControllerServer(grpcServer, s)

	s.logger.Debug("Controller Server listening on", "address", lis.Addr())

	if err := grpcServer.Serve(lis); err != nil {
		s.logger.Error("Controller Server failed to serve", "error", err)
	}
	defer grpcServer.Stop()

}
