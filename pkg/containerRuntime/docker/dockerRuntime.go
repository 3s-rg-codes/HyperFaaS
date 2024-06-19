package dockerRuntime

import (
	"context"
	"fmt"
	"io"
	"os"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
)

type DockerRuntime struct {
	cr.ContainerRuntime
	cli *client.Client
}

const (
	//This docker volume must be created before running the worker
	volumeName = "fn-logs"
	bindDest   = "/root/logs"
	tmpfsDest  = "/root/tmpfs"
)

func NewDockerRuntime() *DockerRuntime {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Error().Msgf("Could not create Docker client: %v", err)
		return nil
	}
	return &DockerRuntime{cli: cli}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, imageTag string, config *pb.Config) (string, error) {
	// Pull the image

	//Check if the image already exists
	// Check if the image is present
	//TODO: make this simpler and cleaner

	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})

	if err != nil {
		return "", fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image
		log.Printf("Pulling image %s", imageTag)
		reader, err := d.cli.ImagePull(ctx, imageTag, image.PullOptions{})

		if err != nil {
			return "", err
		}

		io.Copy(os.Stdout, reader)
		reader.Close()

		log.Printf("Pulled image %s", imageTag)
	}

	// Create the container
	log.Printf("Creating container with image tag %s", imageTag)
	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: "host",
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: volumeName,
				Target: bindDest,
			},
		},
	}, &network.NetworkingConfig{}, nil, "")

	if err != nil {
		return "", err
	}
	log.Printf("Created container with ID %s , Warnings: %v", resp.ID, resp.Warnings)

	// Start the container
	log.Printf("Starting container with ID %s", resp.ID)
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}
	log.Printf("Started container with ID %s", resp.ID)
	return resp.ID, nil
}

func (d *DockerRuntime) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	return &pb.Response{}, nil

}

func (d *DockerRuntime) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {
	if err := d.cli.ContainerStop(ctx, req.Id, container.StopOptions{}); err != nil {
		return nil, err
	}
	return req, nil
}

// TODO Status over docker Volume
func (d *DockerRuntime) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	return nil
}
