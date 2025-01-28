package dockerRuntime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/mount"

	"github.com/google/uuid"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DockerRuntime struct {
	cr.ContainerRuntime
	Cli             *client.Client
	autoRemove      bool
	outputFolderAbs string
	logger          *slog.Logger
}

const (
	logsOutputDir   = "functions/logs/" // Relative to project root
	containerPrefix = "hyperfaas-"
	imagePrefix     = "hyperfaas-"
)

var (
	// Regex that matches all chars that are not valid in a container names
	forbiddenChars = regexp.MustCompile("[^a-zA-Z0-9_.-]")
	environment    string
)

func NewDockerRuntime(autoRemove bool, env string, logger *slog.Logger) *DockerRuntime {
	cli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not create Docker client", "error", err)
		return nil
	}

	environment = env

	// Figure out where to put the logs
	var outputFolderAbs string
	// Get the current path
	currentWd, _ := os.Getwd()
	logger.Info("Current path", "path", currentWd)
	// If the current path ends with /cmd/workerNode, remove it from the path to get the base path of the project
	if strings.HasSuffix(currentWd, "/bin") {
		outputFolderAbs = currentWd[:len(currentWd)-3] + logsOutputDir
	} else {
		for {
			if strings.HasSuffix(currentWd, "HyperFaaS/") {
				outputFolderAbs = currentWd + logsOutputDir
				break
			}
			currentWd = currentWd[:len(currentWd)-1]
		}
	}
	logger.Info("Logs directory", "path", outputFolderAbs)

	// Create the logs directory
	if _, err := os.Stat(outputFolderAbs); os.IsNotExist(err) {
		if err := os.MkdirAll(outputFolderAbs, 0755); err != nil {
			logger.Error("Could not create logs directory", "error", err)
			return nil
		}
	}

	return &DockerRuntime{Cli: cli, autoRemove: autoRemove, outputFolderAbs: outputFolderAbs, logger: logger}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, imageTag string, config *controller.Config) (string, error) {
	// Start by checking if the image exists locally already
	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.Cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})

	if err != nil {
		return "", fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image from docker hub if necessary.
		d.logger.Info("Pulling image", "image", imageTag)
		reader, err := d.Cli.ImagePull(ctx, imageTag, image.PullOptions{})

		if err != nil {
			d.logger.Error("Could not pull image", "image", imageTag, "error", err)
			return "", status.Errorf(codes.NotFound, err.Error())
		}

		_, _ = io.Copy(os.Stdout, reader)
		_ = reader.Close()

		d.logger.Info("Pulled image", "image", imageTag)
	}

	// Create the container
	d.logger.Debug("Creating container", "image", imageTag)
	containerName := containerPrefix + imageTag + "-" + uuid.New().String()[:8]
	// only [a-zA-Z0-9][a-zA-Z0-9_.-] are allowed in the container name, just remove all forbidden characters
	containerName = forbiddenChars.ReplaceAllString(containerName, "")

	var networkMode container.NetworkMode
	var mountType mount.Type
	var source string
	var callerAddress string

	switch environment {
	case "local":
		networkMode = "bridge"
		mountType = mount.TypeBind
		source = d.outputFolderAbs
		callerAddress = "host.docker.internal"
	case "compose":
		networkMode = "hyperfaas-network"
		mountType = mount.TypeVolume
		source = "function-logs"
		callerAddress = "worker"
	}

	resp, err := d.Cli.ContainerCreate(ctx, &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
		Env: []string{
			"CALLER_SERVER_ADDRESS=" + callerAddress,
		},
	}, &container.HostConfig{
		AutoRemove:  d.autoRemove,
		NetworkMode: networkMode,
		Mounts: []mount.Mount{
			{
				Type:   mountType,
				Source: source,
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
		d.logger.Error("Could not create container", "image", imageTag, "error", err)
		return "", err
	}
	d.logger.Debug("Starting container", "id", resp.ID, "warnings", resp.Warnings)
	if err := d.Cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}

	return resp.ID, nil
}

func (d *DockerRuntime) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	//TODO implement container monitoring, if container fails, return error message in call

	return &common.CallResponse{}, nil

}

func (d *DockerRuntime) Stop(ctx context.Context, req *common.InstanceID) (*common.InstanceID, error) {
	// Check if the container exists
	_, err := d.Cli.ContainerInspect(ctx, req.Id)
	if err != nil {
		d.logger.Error("Container does not exist", "id", req.Id)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Stop the container
	if err := d.Cli.ContainerStop(ctx, req.Id, container.StopOptions{}); err != nil {
		return nil, err
	}

	d.logger.Debug("Stopped container", "id", req.Id)

	return req, nil
}

// TODO Status over docker Volume

func (d *DockerRuntime) Status(req *controller.StatusRequest, stream controller.Controller_StatusServer) error {

	return nil
}

// NotifyCrash notifies the caller if the container crashes. It hangs forever until the container either returns (where it returns nil) or crashes (where it returns an error)
func (d *DockerRuntime) NotifyCrash(ctx context.Context, instanceId *common.InstanceID) error {
	opt := events.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "container", Value: instanceId.Id}),
	}
	eventsChan, errChan := d.Cli.Events(ctx, opt)
	for {
		select {
		case event, ok := <-eventsChan:
			if !ok {
				eventsChan = nil
			} else {
				d.logger.Error("Docker event", "instance_id", instanceId.Id, "action", event.Action)
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
