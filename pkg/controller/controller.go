package controller

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/caller"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog/log"
	cpu "github.com/shirou/gopsutil/v4/cpu"
	mem "github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Controller struct {
	pb.UnimplementedControllerServer
	runtime      cr.ContainerRuntime
	CallerServer caller.CallerServer
	StatsManager stats.StatsManager
}

func (s *Controller) Start(ctx context.Context, req *pb.StartRequest) (*pb.InstanceID, error) {

	instanceId, err := s.runtime.Start(ctx, req.ImageTag.Tag, req.Config)

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Container(instanceId).Start().WithStatus("failed"))
		return nil, err

	}
	s.StatsManager.Enqueue(stats.Event().Container(instanceId).Start().WithStatus("success"))

	s.CallerServer.RegisterFunction(instanceId)

	return &pb.InstanceID{Id: instanceId}, nil
}

// This function passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	// Check if the instance ID is present in the FunctionCalls map
	if _, ok := s.CallerServer.FunctionCalls.FcMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if the instance ID is present in the FunctionResponses map
	if _, ok := s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if container crashes
	containerCrashed := make(chan error)
	//defer close(containerCrashed)

	go func() {
		containerCrashed <- s.runtime.NotifyCrash(ctx, req.InstanceId.Id)
	}()

	log.Debug().Msgf("Passing call with payload: %v to channel of instance ID %s", req.Params.Data, req.InstanceId.Id)

	go func() {
		// Pass the call to the channel based on the instance ID
		s.CallerServer.PassCallToChannel(req.InstanceId.Id, req.Params.Data)
		// stats
		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Call().WithStatus("success"))

	}()

	select {

	case data := <-s.CallerServer.FunctionResponses.FrMap[req.InstanceId.Id]:

		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Response().WithStatus("success"))

		log.Debug().Msgf("Extracted response: '%v' from container with instance ID %s", data, req.InstanceId.Id)
		response := &pb.Response{Data: data}
		return response, nil

	case err := <-containerCrashed:

		s.StatsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Die())

		log.Error().Msgf("Container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

		return nil, fmt.Errorf("container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

	}

}

func (s *Controller) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {

	//unregister the function from the maps
	s.CallerServer.UnregisterFunction(req.Id)

	resp, err := s.runtime.Stop(ctx, req)

	if err != nil {
		s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("failed"))
		return nil, err
	}

	s.StatsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("success"))

	return resp, nil

}

// Streams the status updates to a client.
// Using a channel to listen to the stats manager for status updates
// Status Updates are defined in pkg/stats/statusUpdate.go
func (s *Controller) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	//If a node is re-hitting the status endpoint, use the existing channel
	statsChannel := s.StatsManager.GetListenerByID(req.NodeID)

	if statsChannel != nil {
		log.Debug().Msgf("Node %s is re-hitting the status endpoint", req.NodeID)
	} else {

		statsChannel = make(chan stats.StatusUpdate, 10000)
		s.StatsManager.AddListener(req.NodeID, statsChannel)
	}
	for data := range statsChannel {
		// Check if the stream is closed
		if stream.Context().Err() == nil {
			if err := stream.Send(
				&pb.StatusUpdate{
					InstanceId: data.InstanceID,
					Type:       data.Type,
					Event:      data.Event,
					Status:     data.Status,
				}); err != nil {
				log.Error().Err(err).Msgf("Error streaming data to node %s", req.NodeID)
				return err
			}
			log.Debug().Msgf("Sent status update to node %s", req.NodeID)
		} else {
			log.Debug().Msgf("Stream closed for node %s", req.NodeID)
			// re buffer the data
			s.StatsManager.Enqueue(&data)
			return stream.Context().Err()
		}
	}

	return nil
}

func (s *Controller) Metrics(ctx context.Context, req *pb.MetricsRequest) (*pb.MetricsUpdate, error) {

	cpu_percentage_percpu, err1 := cpu.Percent(time.Millisecond*10, true)
	virtual_mem, err2 := mem.VirtualMemory()

	if err1 != nil || err2 != nil {
		return nil, err1
	}
	return &pb.MetricsUpdate{CpuPercentPercpu: cpu_percentage_percpu, UsedRamPercent: virtual_mem.UsedPercent}, nil
}

func New(runtime cr.ContainerRuntime) Controller {

	return Controller{
		runtime:      runtime,
		CallerServer: caller.New(),
		StatsManager: stats.New(),
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
		log.Error().Msgf("failed to listen: %v", err)
	}

	pb.RegisterControllerServer(grpcServer, s)

	log.Debug().Msgf("Controller Server listening on %v", lis.Addr())

	if err := grpcServer.Serve(lis); err != nil {
		log.Error().Msgf("Controller Server failed to serve: %v", err)
	}
	defer grpcServer.Stop()

}
