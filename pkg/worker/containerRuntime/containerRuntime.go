package containerRuntime

import (
	"context"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"io"
)

// ContainerRuntime is an interface for starting and stopping containers.
type ContainerRuntime interface {
	// Start a container with the given image tag and configuration.
	Start(ctx context.Context, imageTag string, config *common.Config) (string, error)

	// Call a container with the given request.
	Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error)

	// Stop a container with the given instance ID.
	Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error)

	// Status returns the status of a container with the given instance ID.
	Status(req *controller.StatusRequest, stream controller.Controller_StatusServer) error

	//NotifyCrash hangs and returns when the container exits: either it returns nil if the container exits normally, or an error if the container crashes.
	NotifyCrash(ctx context.Context, instanceId *common.InstanceID) error

	//RemoveImage checks if the provided image exists locally and removes it if it does
	RemoveImage(ctx context.Context, imageID string) error

	//ContainerExists checks if a container with the given ID currently exists (running or not)
	ContainerExists(ctx context.Context, instanceID string) bool

	//ContainerStats returns the stats for the container with the provided id
	ContainerStats(ctx context.Context, containerID string) io.ReadCloser
}
