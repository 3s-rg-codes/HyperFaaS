package controller

import (
	"context"
	"fmt"
	"net"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

type Controller struct {
	pb.UnimplementedControllerServer
	runtime cr.ContainerRuntime
}

func (s *Controller) Start(ctx context.Context, req *pb.StartRequest) (*pb.InstanceID, error) {

	instanceId, err := s.runtime.RuntimeStart(ctx, req.ImageTag.Tag, req.Config)

	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	return &pb.InstanceID{Id: instanceId}, nil
}

// This function passes the call through the channel of the instance ID in the FunctionCalls map
// runtime.Call is also called to check for errors
func (s *Controller) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	response, err := s.runtime.RuntimeCall(ctx, req)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (s *Controller) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {

	resp, err := s.runtime.RuntimeStop(ctx, req)

	if err != nil {
		return nil, err
	}

	return resp, nil

}

// Streams the status updates to a client.
// Using a channel to listen to the stats manager for status updates
// Status Updates are defined in pkg/stats/statusUpdate.go
func (s *Controller) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	err := s.runtime.RuntimeStatus(req, stream)
	if err != nil {
		return err
	}

	return nil
}

func New(runtime cr.ContainerRuntime) Controller {

	return Controller{
		runtime: runtime,
	}

}

func (s *Controller) StartServer() {

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
