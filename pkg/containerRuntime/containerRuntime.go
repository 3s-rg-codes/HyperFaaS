package containerRuntime

import (
	"context"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
)

// ContainerRuntime is an interface for starting and stopping containers.
type ContainerRuntime interface {
	// Start a container with the given image tag and configuration.
	Start(ctx context.Context, imageTag string, config *pb.Config) (string, error)
	// Call a container with the given request.
	Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error)
	// Stop a container with the given instance ID.
	Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error)
	// Status returns the status of a container with the given instance ID.
	Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error

	//NotifyCrash hangs and returns when the container exits: either it returns nil if the container exits normally, or an error if the container crashes.
	NotifyCrash(ctx context.Context, instanceId string) error
}
