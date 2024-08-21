package dockerRuntime

import (
	"context"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/caller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/stats"
	"github.com/google/uuid"
	"io"
	"os"
	"regexp"
	"strings"

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
	Cli             *client.Client
	autoRemove      bool
	outputFolderAbs string
	callerServer    *caller.CallerServer
	statsManager    *stats.StatsManager
}

const (
	logsOutputDir   = "functions/logs/" // Relative to project root
	containerPrefix = "hyperfaas-"
	imagePrefix     = "hyperfaas-"
)

var (
	// Regex that matches all chars that are not valid in a container name
	forbiddenChars = regexp.MustCompile("[^a-zA-Z0-9_.-]")
)

func NewDockerRuntime(autoRemove bool, caller *caller.CallerServer, stats *stats.StatsManager) *DockerRuntime {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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

	return &DockerRuntime{
		Cli:             cli,
		autoRemove:      autoRemove,
		outputFolderAbs: outputFolderAbs,
		callerServer:    caller,
		statsManager:    stats,
	}
}

// Start a container with the given image tag and configuration.
func (d *DockerRuntime) RuntimeStart(ctx context.Context, imageTag string, config *pb.Config) (string, error) {
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
		//s.runtime.statsManager.Enqueue(stats.Event().Container(instanceId).Start().WithStatus("failed"))
		//TODO this line doesnt make sense because if the container didnt start we dont have na id to log the fail to, how else can we do this
		return "", err
	}
	resp, err := d.Cli.ContainerCreate(ctx, &container.Config{
		Image: imageTag,
		ExposedPorts: nat.PortSet{
			"50052/tcp": struct{}{},
		},
	}, &container.HostConfig{
		AutoRemove:  d.autoRemove,
		NetworkMode: "host",
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: d.outputFolderAbs,
				Target: "/logs/",
			},
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

	d.statsManager.Enqueue(stats.Event().Container(resp.ID).Start().WithStatus("success"))
	d.callerServer.RegisterFunction(resp.ID)

	return resp.ID, nil
}

func (d *DockerRuntime) RuntimeCall(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {

	//Check if the instance ID is present in the FunctionCalls map

	if _, ok := d.callerServer.FunctionCalls.FcMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if the instance ID is present in the FunctionResponses ma
	if _, ok := d.callerServer.FunctionResponses.FrMap[req.InstanceId.Id]; !ok {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Check if container crashes
	containerCrashed := make(chan error)
	//defer close(containerCrashed)

	go func() {
		containerCrashed <- d.RuntimeNotifyCrash(ctx, req.InstanceId.Id)
	}()

	log.Debug().Msgf("Passing call with payload: %v to channel of instance ID %s", req.Params.Data, req.InstanceId.Id)

	go func() {
		// Pass the call to the channel based on the instance ID
		d.callerServer.PassCallToChannel(req.InstanceId.Id, req.Params.Data)

		fmt.Println("Passed call to channel")
		// stats
		d.statsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Call().WithStatus("success"))

		//fmt.Println("Enqueued stats")

	}()

	select {

	case data := <-d.callerServer.FunctionResponses.FrMap[req.InstanceId.Id]:

		d.statsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Response().WithStatus("success"))

		log.Debug().Msgf("Extracted response: '%v' from container with instance ID %s", data, req.InstanceId.Id)
		response := &pb.Response{Data: data}
		return response, nil

	case err := <-containerCrashed:

		d.statsManager.Enqueue(stats.Event().Container(req.InstanceId.Id).Die())

		log.Error().Msgf("Container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

		return nil, fmt.Errorf("container crashed while waiting for response from container with instance ID %s , Error message: %v", req.InstanceId.Id, err)

	}

}

func (d *DockerRuntime) RuntimeStop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {

	d.callerServer.UnregisterFunction(req.Id)

	// Check if the container exists
	_, err := d.Cli.ContainerInspect(ctx, req.Id)
	if err != nil {
		log.Error().Msgf("Container %s does not exist", req.Id)
		d.statsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("failed"))
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	// Stop the container
	if err := d.Cli.ContainerStop(ctx, req.Id, container.StopOptions{}); err != nil {
		d.statsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("failed"))
		return nil, err
	}

	log.Debug().Msgf("Stopped container with instance ID %s", req.Id)

	d.statsManager.Enqueue(stats.Event().Container(req.Id).Stop().WithStatus("success"))

	return req, nil
}

func (d *DockerRuntime) RuntimeStatus(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	//If a node is re-hitting the status endpoint, use the existing channel
	statsChannel := d.statsManager.GetListenerByID(req.NodeID)

	if statsChannel != nil {
		log.Debug().Msgf("Node %s is re-hitting the status endpoint", req.NodeID)
	} else {

		statsChannel = make(chan stats.StatusUpdate)
		d.statsManager.AddListener(req.NodeID, statsChannel)
	}
	for data := range statsChannel {

		if err := stream.Send(

			&pb.StatusUpdate{
				InstanceId: data.InstanceID,
				Type:       data.Type,
				Event:      data.Event,
				Status:     data.Status,
			}); err != nil {
			log.Error().Err(err).Msgf("Error streaming data to node %s", req.NodeID)
			return err
		}
	}

	return nil
}

// RuntimeNotifyCrash notifies the caller if the container crashes. It hangs forever until the container either returns (where it returns nil) or crashes (where it returns an error)
func (d *DockerRuntime) RuntimeNotifyCrash(ctx context.Context, instanceId string) error {
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
