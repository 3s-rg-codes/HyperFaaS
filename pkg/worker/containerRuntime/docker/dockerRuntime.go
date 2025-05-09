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
	Cli                 *client.Client
	autoRemove          bool
	containerized       bool
	callerServerAddress string
	outputFolderAbs     string
	logger              *slog.Logger
}

const (
	logsOutputDir   = "/functions/logs/" // Relative to project root
	containerPrefix = "hyperfaas-"
	imagePrefix     = "hyperfaas-"
)

var (
	// Regex that matches all chars that are not valid in a container names
	forbiddenChars = regexp.MustCompile("[^a-zA-Z0-9_.-]")
)

func NewDockerRuntime(containerized bool, autoRemove bool, callerServerAddress string, logger *slog.Logger) *DockerRuntime {
	var clientOpt client.Opt
	if containerized {
		clientOpt = client.WithHost("unix:///var/run/docker.sock")
	} else {
		clientOpt = client.FromEnv
	}
	cli, err := client.NewClientWithOpts(clientOpt, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not create Docker client", "error", err)
		return nil
	}

	// Figure out where to put the logs
	var outputFolderAbs string
	// Get the current path
	currentWd, _ := os.Getwd()
	logger.Debug("Current path", "path", currentWd)
	// If the current path ends with /cmd/workerNode, remove it from the path to get the base path of the project
	if strings.HasSuffix(currentWd, "/bin") {
		outputFolderAbs = currentWd[:len(currentWd)-4] + logsOutputDir
	} else {
		for {
			if strings.HasSuffix(currentWd, "HyperFaaS") || len(currentWd) == 0 {
				outputFolderAbs = currentWd + logsOutputDir
				break
			}
			currentWd = currentWd[:len(currentWd)-1]
		}
	}
	logger.Debug("Logs directory", "path", outputFolderAbs)

	// Create the logs directory
	if _, err := os.Stat(outputFolderAbs); os.IsNotExist(err) {
		if err := os.MkdirAll(outputFolderAbs, 0755); err != nil {
			logger.Error("Could not create logs directory", "error", err)
			return nil
		}
	}

	return &DockerRuntime{Cli: cli, autoRemove: autoRemove, outputFolderAbs: outputFolderAbs, logger: logger, containerized: containerized, callerServerAddress: callerServerAddress}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, functionID string, imageTag string, config *common.Config) (string, error) {
	// Start by checking if the image exists locally already
	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.Cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})

	if err != nil {
		return "", fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image from docker hub if necessary.
		d.logger.Debug("Pulling image", "image", imageTag)
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

	resp, err := d.Cli.ContainerCreate(ctx, d.createContainerConfig(imageTag, functionID), d.createHostConfig(config), &network.NetworkingConfig{}, nil, containerName)

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
		case event := <-eventsChan:
			if event.Action == "die" {
				return fmt.Errorf("container died")
			}
		case <-errChan:
			// Ignore Docker event errors as they're usually not critical
			continue
		case <-ctx.Done():
			d.logger.Debug("Crash context done")
			return nil
		}
	}
}

func (d *DockerRuntime) RemoveImage(ctx context.Context, imageTag string) error {

	opt := image.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "reference", Value: imageTag}),
	}

	localImages, err := d.Cli.ImageList(ctx, opt)

	if err != nil {
		d.logger.Error("Could not list local images", "error", err)
		return fmt.Errorf("could not list local images, error: %v", err)
	}

	if len(localImages) > 0 {
		d.logger.Debug("Image already exists locally", "image", imageTag)
		//erase image
		_, err := d.Cli.ImageRemove(ctx, localImages[0].ID, image.RemoveOptions{
			Force: true,
		})
		if err != nil {
			d.logger.Error("Could not remove local image", "error", err)
			return fmt.Errorf("could not delete local image, error: %v", err)
		}
	}

	return nil
}

func (d *DockerRuntime) ContainerExists(ctx context.Context, instanceID string) bool {
	_, err := d.Cli.ContainerInspect(ctx, instanceID)
	return err == nil
}

func (d *DockerRuntime) ContainerStats(ctx context.Context, containerID string) io.ReadCloser { //TODO: we need to find a return type that is compatible with all container runtimes and makes sense
	st, _ := d.Cli.ContainerStats(ctx, containerID, false)
	return st.Body
}

func (d *DockerRuntime) createContainerConfig(imageTag string, functionID string) *container.Config {
	return &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
		Env: []string{
			fmt.Sprintf("CALLER_SERVER_ADDRESS=%s", d.getCallerServerAddress()),
			fmt.Sprintf("FUNCTION_ID=%s", functionID),
		},
	}
}

func (d *DockerRuntime) createHostConfig(config *common.Config) *container.HostConfig {
	var networkMode string
	if d.containerized {
		networkMode = "hyperfaas-network"
	} else {
		networkMode = "bridge" //Cannot be host since otherwise the container id pulled by the docker container from env will always be docker-desktop
	}
	return &container.HostConfig{
		AutoRemove:  d.autoRemove,
		NetworkMode: container.NetworkMode(networkMode),
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: "function-logs",
				Target: "/logs/",
			},
		},
		Resources: container.Resources{
			Memory:    config.Memory,
			CPUPeriod: config.Cpu.Period,
			CPUQuota:  config.Cpu.Quota,
		},
	}
}

func (d *DockerRuntime) getCallerServerAddress() string {
	// Containerized mode uses docker network dns to resolve the caller server address, hence we need to replace the localhost/127.0.0.1/0.0.0.0 with worker
	if d.containerized {
		address := d.callerServerAddress
		address = strings.Replace(address, "localhost", "worker", 1)
		address = strings.Replace(address, "127.0.0.1", "worker", 1)
		address = strings.Replace(address, "0.0.0.0", "worker", 1)
		return address
	}
	// Replace localhost/127.0.0.1 with host.docker.internal for non-containerized mode
	address := d.callerServerAddress
	address = strings.Replace(address, "localhost", "host.docker.internal", 1)
	address = strings.Replace(address, "127.0.0.1", "host.docker.internal", 1)
	return address
}
