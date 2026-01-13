package containerRuntime

import (
	"context"
	"io"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type Container struct {
	Id         string
	InternalIP string
	ExternalIP string
	Name       string
}

// ContainerRuntime is an interface for starting and stopping containers.
type ContainerRuntime interface {
	// Start a container with the given image tag and configuration. returns the container id it's ips and the container name
	Start(ctx context.Context, functionID string, imageTag string, config *common.Config) (Container, error)

	// Stop a container with the given instance ID.
	Stop(ctx context.Context, instanceID string) error
	// MonitorContainer monitors a container and returns a specific event according to the container's exit status. Blocks until the container exits.
	MonitorContainer(ctx context.Context, instanceId string, functionId string) (ContainerEvent, error)

	// RemoveImage checks if the provided image exists locally and removes it if it does
	RemoveImage(ctx context.Context, imageID string) error

	// ContainerExists checks if a container with the given ID currently exists (running or not)
	ContainerExists(ctx context.Context, instanceID string) bool

	// ContainerStats returns the stats for the container with the provided id
	ContainerStats(ctx context.Context, containerID string) io.ReadCloser
}

type ContainerEvent int

const (
	ContainerEventExit ContainerEvent = iota
	ContainerEventOOM
	ContainerEventCrash
)
