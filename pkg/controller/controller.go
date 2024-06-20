package controller

import (
	"context"
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

func (s *Controller) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	log.Debug().Msgf("Passing call with payload: %v to channel of instance ID %s", req.Params.Data, req.InstanceId.Id)

	go func() {

		// Pass the call to the channel based on the instance ID
		s.callerServer.FunctionCalls[req.InstanceId.Id] <- req.Params.Data

		log.Debug().Msgf("Successfully passed call to channel of instance ID %s", req.InstanceId.Id)

	}()

	// Wait for the response
	data := <-s.callerServer.FunctionResponses[req.InstanceId.Id]

	log.Debug().Msgf("Extracted response: '%v' from container with instance ID %s", data, req.InstanceId.Id)

	response := &pb.Response{Data: data}

	return response, nil
}

func (s *Controller) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {

	//unregister the function from the maps
	s.callerServer.UnregisterFunction(req.Id)

	log.Debug().Msgf("Stopping container with instance ID %s", req.Id)

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
