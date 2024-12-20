package dockerRuntime

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/mount"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DockerRuntime struct {
	cr.ContainerRuntime
	Cli             *client.Client
	autoRemove      bool
	outputFolderAbs string
}

const (
	logsOutputDir   = "functions/logs/" // Relative to project root
	containerPrefix = "hyperfaas-"
	imagePrefix     = "hyperfaas-"
)

var (
	// Regex that matches all chars that are not valid in a container names
	forbiddenChars = regexp.MustCompile("[^a-zA-Z0-9_.-]")
)

func NewDockerRuntime(autoRemove bool, cs *caller.CallerServer) *DockerRuntime {
	cli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		log.Error().Msgf("Could not create Docker client: %v", err)
		return nil
	}

	// Figure out where to put the logs
	var outputFolderAbs string
	// Get the current path
	currentWd, _ := os.Getwd()
	log.Info().Msgf("Current path: %s", currentWd)
	// If the current path ends with /cmd/workerNode, remove it from the path to get the base path of the project
	if strings.HasSuffix(currentWd, "cmd/workerNode") {
		outputFolderAbs = currentWd[:len(currentWd)-14] + logsOutputDir
	} else {
		outputFolderAbs = currentWd + "/" + logsOutputDir
	}
	log.Info().Msgf("Logs directory: %s", outputFolderAbs)

	// Create the logs directory
	if _, err := os.Stat(logsOutputDir); os.IsNotExist(err) {
		if err := os.MkdirAll(logsOutputDir, 0755); err != nil {
			log.Error().Msgf("Could not create logs directory: %v", err)
			return nil
		}
	}

	return &DockerRuntime{Cli: cli, autoRemove: autoRemove, outputFolderAbs: outputFolderAbs}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, imageTag string, config *pb.Config) (string, error) {
	// Start by checking if the image exists locally already
	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.Cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})

	if err != nil {
		return "", fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image from docker hub if necessary.
		log.Printf("Pulling image %s", imageTag)
		reader, err := d.Cli.ImagePull(ctx, imageTag, image.PullOptions{})

		if err != nil {
			log.Err(err).Msgf("Could not pull image %s", imageTag)
			return "", status.Errorf(codes.NotFound, err.Error())
		}

		_, _ = io.Copy(os.Stdout, reader)
		_ = reader.Close()

		log.Info().Msgf("Pulled image %s", imageTag)
	}

	// Create the container
	log.Debug().Msgf("Creating container with image tag %s", imageTag)
	containerName := containerPrefix + imageTag + "-" + uuid.New().String()[:8]
	// only [a-zA-Z0-9][a-zA-Z0-9_.-] are allowed in the container name, just remove all forbidden characters
	containerName = forbiddenChars.ReplaceAllString(containerName, "")
	if err != nil {
		log.Err(err).Msgf("Could not get absolute path of logs directory: %v", err)
		return "", err
	}
	resp, err := d.Cli.ContainerCreate(ctx, &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
	}, &container.HostConfig{
		AutoRemove:  d.autoRemove,
		NetworkMode: "hyperfaas-network",
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: "function-logs",
				Target: "/logs/",
			},
		},
		Resources: container.Resources{
			//Memory:    int64(config.Memory),
			//CPUPeriod: int64(config.Cpu.Period),
			//CPUQuota:  int64(config.Cpu.Quota),
		},
	}, &network.NetworkingConfig{}, nil, containerName)

	if err != nil {
		log.Err(err).Msgf("Could not create container with image tag %s", imageTag)
		return "", err
	}
	log.Debug().Msgf("Created and now Starting container with ID %s , Warnings: %v", resp.ID, resp.Warnings)
	if err := d.Cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}

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

// NotifyCrash notifies the caller if the container crashes. It hangs forever until the container either returns (where it returns nil) or crashes (where it returns an error)
func (d *DockerRuntime) NotifyCrash(ctx context.Context, instanceId string) error {
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
				log.Error().Msgf("DOCKEREVENT - Event instance ID %v: %s\n", instanceId, event.Action)
				if event.Action == "die" {
					return fmt.Errorf("container died")
				}
			}
		//When a function call is successful, Docker events  retruns an annoying error that we can ignore.
		//WARNING: Maybe there can be other errors that we should not ignore.
		//TODO: Find a better way to handle this
		case _, ok := <-errChan:
			if !ok {
				errChan = nil
			}
		case <-ctx.Done():
			return nil
		}

		// return if both channels are closed
		if eventsChan == nil && errChan == nil {
			return nil
		}
	}
}
