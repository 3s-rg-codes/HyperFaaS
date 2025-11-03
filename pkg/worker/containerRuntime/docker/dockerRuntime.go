package dockerRuntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/mount"

	"github.com/google/uuid"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
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
	// the docker client
	Cli *client.Client
	// whether the container should be automatically removed when it exits
	autoRemove bool
	// whether the runtime runs inside a containerized environment
	containerized bool
	// the address of the worker server
	workerAddress string
	// the absolute path to the output folder for logs
	outputFolderAbs string

	// the name of the docker service used to run the container if containerized is true
	serviceName string
	// the name of the docker network for function containers
	networkName string
	// the logger
	logger *slog.Logger
}

const (
	logsOutputDir   = "/functions/logs/" // Relative to project root
	containerPrefix = "hyperfaas-"
	imagePrefix     = "hyperfaas-"
	// This is the default IP of the docker bridge network gateway, which should be used by containers to connect to the worker server if we are running in non-containerized mode
	dockerBridgeGatewayIP = "172.17.0.1"
)

// Regex that matches all chars that are not valid in a container names
var forbiddenChars = regexp.MustCompile("[^a-zA-Z0-9_.-]")

// NewDockerRuntime creates a new DockerRuntime instance. It can be containerized, meaning that it considers that this code will run inside a container, which changes how we address the docker API and how we manage networks.
func NewDockerRuntime(containerized bool, autoRemove bool, workerAddress string, logger *slog.Logger, serviceName string, networkName string) *DockerRuntime {
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
		if err := os.MkdirAll(outputFolderAbs, 0o755); err != nil {
			logger.Error("Could not create logs directory", "error", err)
			return nil
		}
	}

	return &DockerRuntime{Cli: cli, autoRemove: autoRemove, outputFolderAbs: outputFolderAbs, logger: logger, containerized: containerized, workerAddress: workerAddress, serviceName: serviceName, networkName: networkName}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) Start(ctx context.Context, functionID string, imageTag string, config *common.Config) (cr.Container, error) {
	// Start by checking if the image exists locally already
	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageTag)
	images, err := d.Cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})
	if err != nil {
		return cr.Container{}, fmt.Errorf("could not list Docker images: %v", err)
	}

	if len(images) == 0 {
		// Pull the image from docker hub if necessary.
		d.logger.Debug("Pulling image", "image", imageTag)
		reader, err := d.Cli.ImagePull(ctx, imageTag, image.PullOptions{})
		if err != nil {
			d.logger.Error("Could not pull image", "image", imageTag, "error", err)
			return cr.Container{}, status.Error(codes.NotFound, err.Error())
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

	resp, err := d.Cli.ContainerCreate(ctx,
		d.createContainerConfig(imageTag, functionID, config.Timeout),
		d.createHostConfig(config),
		&network.NetworkingConfig{},
		nil,
		containerName,
	)
	if err != nil {
		d.logger.Error("Could not create container", "image", imageTag, "error", err)
		return cr.Container{}, err
	}

	d.logger.Debug("Starting container", "id", resp.ID, "warnings", resp.Warnings)
	if err := d.Cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return cr.Container{}, err
	}

	internalIP, externalIP, err := d.resolveContainerAddrs(ctx, resp.ID, containerName)
	d.logger.Debug("Resolved container address", "id", resp.ID, "internal", internalIP, "external", externalIP)
	if err != nil {
		return cr.Container{}, err
	}
	shortID := resp.ID[:12]

	return cr.Container{Id: shortID, Name: containerName, InternalIP: internalIP, ExternalIP: externalIP}, nil
}

// Stop stops a container with the given instance ID. It returns once the container is no longer running.
func (d *DockerRuntime) Stop(ctx context.Context, instanceID string) error {
	// Check if the container exists
	_, err := d.Cli.ContainerInspect(ctx, instanceID)
	if err != nil {
		d.logger.Error("Container does not exist", "id", instanceID)
		return status.Error(codes.NotFound, err.Error())
	}

	// Stop the container
	if err := d.Cli.ContainerStop(ctx, instanceID, container.StopOptions{}); err != nil {
		return err
	}

	d.Cli.ContainerWait(ctx, instanceID, container.WaitConditionRemoved)

	d.logger.Debug("Stopped container", "id", instanceID)

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
		case event := <-eventsChan:
			if event.Action == "die" {
				return errors.New("container died")
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

// RemoveImage removes an image from the local Docker registry
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
		// erase image
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

// ContainerExists checks if a container with the given ID is currently running
func (d *DockerRuntime) ContainerExists(ctx context.Context, instanceID string) bool {
	containerJSON, err := d.Cli.ContainerInspect(context.Background(), instanceID)
	if err != nil {
		d.logger.Error("Error inspecting container", "error", err)
		return false
	}

	if containerJSON.State.Running {
		return true
	}
	return false
}

func (d *DockerRuntime) ContainerStats(ctx context.Context, containerID string) io.ReadCloser { // TODO: we need to find a return type that is compatible with all container runtimes and makes sense
	st, _ := d.Cli.ContainerStats(ctx, containerID, false)
	return st.Body
}

func (d *DockerRuntime) createContainerConfig(imageTag string, functionID string, timeoutSeconds int32) *container.Config {
	var a string
	port := strings.Split(d.workerAddress, ":")[1]
	if d.containerized {
		// this depends on the compose.yaml , the name of the service is worker
		a = fmt.Sprintf("%s:%s", d.serviceName, port)
	} else {
		a = fmt.Sprintf("%s:%s", dockerBridgeGatewayIP, port)
	}
	return &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
		Env: []string{
			"FUNCTION_ID=" + functionID,
			// so the container can connect to the worker server and send a ReadySignal
			"CONTROLLER_ADDRESS=" + a,
			// after how many seconds without requests the container should timeout
			"TIMEOUT_SECONDS=" + strconv.Itoa(int(timeoutSeconds)),
		},
	}
}

func (d *DockerRuntime) createHostConfig(config *common.Config) *container.HostConfig {
	var networkMode string
	if d.containerized {
		networkMode = d.networkName
		// networkMode = "host"
	} else {
		networkMode = "bridge" // Cannot be host since otherwise the container id pulled by the docker container from env will always be docker-desktop
	}
	return &container.HostConfig{
		AutoRemove:      d.autoRemove,
		NetworkMode:     container.NetworkMode(networkMode),
		PublishAllPorts: true,
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

// resolveContainerAddrs resolves the addresses of the container , considering containerized or non-containerized mode. Docker networks have DNS which allows us to use the container name to resolve the address.
func (d *DockerRuntime) resolveContainerAddrs(ctx context.Context, containerID string, containerName string) (string, string, error) {
	containerJSON, err := d.Cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", "", err
	}

	ports, ok := containerJSON.NetworkSettings.Ports["50052/tcp"]
	if !ok {
		return "", "", fmt.Errorf("container not exposed on port 50052")
	}

	externalIP := ports[0].HostIP + ":" + ports[0].HostPort

	if d.containerized {
		return fmt.Sprintf("%s:%d", containerName, 50052), externalIP, nil
	}

	network, ok := containerJSON.NetworkSettings.Networks[d.networkName]
	if !ok {
		network, ok = containerJSON.NetworkSettings.Networks["bridge"]
		if !ok {
			return "", "", fmt.Errorf("container not connected to %s network or bridge network", d.networkName)
		}
	}

	return fmt.Sprintf("%s:%d", network.IPAddress, 50052), externalIP, nil
}

// MonitorContainer monitors a container and returns when it exits
// Returns nil for timeout, error for crash
func (d *DockerRuntime) MonitorContainer(ctx context.Context, instanceId string, functionId string) (cr.ContainerEvent, error) {
	opt := events.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "container", Value: instanceId}),
	}
	eventsChan, errChan := d.Cli.Events(ctx, opt)

	for {
		select {
		case event := <-eventsChan:

			switch event.Action {
			case events.ActionDie:
				// Get container exit code to determine if it was timeout or crash
				containerJSON, err := d.Cli.ContainerInspect(ctx, instanceId)
				if err != nil {
					d.logger.Error("Failed to inspect container", "id", instanceId, "error", err)
					return cr.ContainerEventExit, fmt.Errorf("container died but failed to inspect: %v", err)
				}

				exitCode := containerJSON.State.ExitCode
				if exitCode == 0 {
					// Exit code 0 = graceful shutdown = timeout
					d.logger.Debug("Container timed out gracefully", "id", instanceId, "exitCode", exitCode)
					return cr.ContainerEventTimeout, nil
				} else {
					// Non-zero exit code = crash
					d.logger.Debug("Container crashed", "id", instanceId, "exitCode", exitCode)
					return cr.ContainerEventCrash, nil
				}
			case events.ActionOOM:
				d.logger.Debug("Container ran out of memory", "id", instanceId)
				return cr.ContainerEventOOM, nil
			}
		case <-errChan:
			// Ignore Docker event errors as they're usually not critical
			continue
		case <-ctx.Done():
			return cr.ContainerEventExit, nil
		}
	}
}
