package controller

import (
	"context"
	"fmt"
	"net"

	"github.com/3s-rg-codes/HyperFaaS/pkg/caller"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

type Controller struct {
	pb.UnimplementedControllerServer
	runtime      cr.ContainerRuntime
	callerServer caller.CallerServer
}

func (s *Controller) Start(ctx context.Context, req *pb.StartRequest) (*pb.InstanceID, error) {

	log.Debug().Msgf("Starting container with image tag %s", req.ImageTag.Tag)
	instanceId, err := s.runtime.Start(ctx, req.ImageTag.Tag, req.Config)

	if err != nil {
		return nil, err
	}

	s.callerServer.RegisterFunction(instanceId)

	return &pb.InstanceID{Id: instanceId}, nil
}

// This function passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	// Check if the instance ID is present in the FunctionCalls map
	if _, ok := s.callerServer.FunctionCalls[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s not found in FunctionCalls map", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, err
	}

	// Check if the instance ID is present in the FunctionResponses map
	if _, ok := s.callerServer.FunctionResponses[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s not found in FunctionResponses map", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, err
	}

	// Check if container crashes
	containerCrashed := make(chan error)

	go func() {
		containerCrashed <- s.runtime.NotifyCrash(ctx, req.InstanceId.Id)
	}()

	log.Debug().Msgf("Passing call with payload: %v to channel of instance ID %s", req.Params.Data, req.InstanceId.Id)

	go func() {
		// Pass the call to the channel based on the instance ID
		s.callerServer.FunctionCalls[req.InstanceId.Id] <- req.Params.Data

		log.Debug().Msgf("Successfully passed call to channel of instance ID %s", req.InstanceId.Id)

	}()

	select {

	case data := <-s.callerServer.FunctionResponses[req.InstanceId.Id]:

		log.Debug().Msgf("Extracted response: '%v' from container with instance ID %s", data, req.InstanceId.Id)
		response := &pb.Response{Data: data}
		return response, nil

	case err := <-containerCrashed:

		log.Error().Msgf("Container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

		return nil, fmt.Errorf("Container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

	}

}

func (s *Controller) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {

	//unregister the function from the maps
	s.callerServer.UnregisterFunction(req.Id)

	return s.runtime.Stop(ctx, req)

}

func (s *Controller) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {
	// TODO
	for {
		stream.Send(&pb.StatusUpdate{InstanceId: req.InstanceId.Id, Status: "some-status"})
	}
}

func New(runtime cr.ContainerRuntime) Controller {

	return Controller{
		runtime:      runtime,
		callerServer: caller.New(),
	}

}

func (s *Controller) StartServer() {

	//Start the caller server
	go func() {
		s.callerServer.Start()
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
