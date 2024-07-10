package dockerRuntime

import (
	"context"
	"fmt"
	"io"
	"os"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DockerRuntime struct {
	cr.ContainerRuntime
	Cli *client.Client
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
	return &DockerRuntime{Cli: cli}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, imageTag string, config *pb.Config) (string, error) {
	// Pull the image

	//Check if the image already exists
	// Check if the image is present
	//TODO: make this simpler and cleaner

	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.Cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})

	if err != nil {
		return "", fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image
		log.Printf("Pulling image %s", imageTag)
		reader, err := d.Cli.ImagePull(ctx, imageTag, image.PullOptions{})

		if err != nil {
			return "", status.Errorf(codes.NotFound, err.Error())
		}

		io.Copy(os.Stdout, reader)
		reader.Close()

		log.Printf("Pulled image %s", imageTag)
	}

	// Create the container
	log.Printf("Creating container with image tag %s", imageTag)
	resp, err := d.Cli.ContainerCreate(ctx, &container.Config{
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
	if err := d.Cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}
	log.Printf("Started container with ID %s", resp.ID)
	return resp.ID, nil
}

func (d *DockerRuntime) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	//TODO implement container monitoring, if container fails, return error message in call

	return &pb.Response{}, nil

}

func (d *DockerRuntime) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {
	// Check if the container exists
	_, err := d.Cli.ContainerInspect(ctx, req.Id)
	if err != nil {
		log.Error().Msgf("Container %s does not exist", req.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Stop the container
	if err := d.Cli.ContainerStop(ctx, req.Id, container.StopOptions{}); err != nil {
		return nil, err
	}

	log.Debug().Msgf("Stopped container with instance ID %s", req.Id)

	return req, nil
}

// TODO Status over docker Volume

func (d *DockerRuntime) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	return nil
}

func (d *DockerRuntime) NotifyCrash(ctx context.Context, instanceId string) error {
	//Print all events to the log
	//Events are this d.Cli.Events(ctx,	events.ListOptions{})
	//eventsChan, errChan := d.Cli.Events(ctx, events.ListOptions{})
	opt := events.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "container", Value: instanceId}),
	}
	eventsChan, errChan := d.Cli.Events(ctx, opt)
	for {
		select {
		case event, ok := <-eventsChan:
			if !ok {
				eventsChan = nil
			} else {
				log.Error().Msgf("DOCKEREVENT - Event instance ID %v: %s\n", instanceId, event.Status)
				if event.Status == "die" {
					return fmt.Errorf("Container died")
				}
			}
		//When a function call is successful, Docker events  retruns an annoying error that we can ignore.
		//WARNING: Maybe there can be other errors that we should not ignore.
		//TODO: Find a better way to handle this
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
			} else {
				// Ignore the error
				//log.Error().Msgf("Error: %v\n", err)
				log.Debug().Str("instanceId", instanceId).Err(err).Msg("Ignoring error from Docker events")
			}
		case <-ctx.Done():
			log.Debug().Msg("Context cancelled, exiting")
			return nil
		}

		// Exit the loop if both channels are closed
		if eventsChan == nil && errChan == nil {
			break
		}
	}

	return nil
}
