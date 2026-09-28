package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
)

const (
	functionPort                 = 50052
	containerPrefix              = "hyperfaas-"
	instanceIDLabel              = "hyperfaas.instance_id"
	functionIDLabel              = "hyperfaas.function_id"
	dockerBridgeGatewayIP        = "172.17.0.1"
	defaultCPUPeriod      uint64 = 100000
	// Docker's default graceful-stop timeout is 10 seconds. The leaf also uses a
	// 10-second StopSandbox RPC timeout, so relying on Docker's default can make
	// the RPC fail just before Docker reports that the container stopped. Keep
	// the grace period shorter and leave time for cleanup and the RPC response.
	maxStopGracePeriod    = 5 * time.Second
	stopCompletionReserve = time.Second
)

var forbiddenChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// Config configures the Docker sandbox runtime for a native worker process.
type Config struct {
	AutoRemove          bool
	NetworkName         string
	WorkerListenAddress string
}

// Runtime manages function sandboxes via the local Docker daemon.
type Runtime struct {
	cli    *client.Client
	cfg    Config
	logger *slog.Logger

	mu         sync.RWMutex
	containers map[uint64]string
}

func New(cfg Config, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		return nil, fmt.Errorf("docker runtime: logger is required")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker runtime: create client: %w", err)
	}
	return &Runtime{
		cli:        cli,
		cfg:        cfg,
		logger:     logger,
		containers: make(map[uint64]string),
	}, nil
}

func (d *Runtime) Prepare(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	imageRef := function.GetRuntime().GetImage()
	if imageRef == "" {
		return nil, status.Error(codes.InvalidArgument, "function runtime image is required")
	}
	if err := d.ensureImage(ctx, imageRef); err != nil {
		return nil, err
	}
	return &core.PreparedArtifact{
		FunctionId: function.GetFunctionId(),
		Image:      imageRef,
	}, nil
}

func (d *Runtime) HasImage(ctx context.Context, imageRef string) (bool, error) {
	imageListArgs := filters.NewArgs()
	imageListArgs.Add("reference", imageRef)
	images, err := d.cli.ImageList(ctx, image.ListOptions{Filters: imageListArgs})
	if err != nil {
		return false, fmt.Errorf("docker runtime: list images: %w", err)
	}
	return len(images) > 0, nil
}

func (d *Runtime) Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	function := req.GetFunction()
	if function == nil {
		return nil, status.Error(codes.InvalidArgument, "function is required")
	}
	instanceID := req.GetInstanceId()
	if instanceID == 0 {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}

	imageRef := function.GetRuntime().GetImage()
	if imageRef == "" {
		if artifact := req.GetArtifact(); artifact != nil && artifact.GetImage() != "" {
			imageRef = artifact.GetImage()
		}
	}
	if imageRef == "" {
		return nil, status.Error(codes.InvalidArgument, "function runtime image is required")
	}
	if err := d.ensureImage(ctx, imageRef); err != nil {
		return nil, err
	}

	containerName := forbiddenChars.ReplaceAllString(
		containerPrefix+sanitizeImageName(imageRef)+"-"+uuid.New().String()[:8],
		"",
	)

	controllerAddress := d.controllerAddress(ctx)
	resp, err := d.cli.ContainerCreate(ctx,
		d.createContainerConfig(function, imageRef, instanceID, controllerAddress),
		d.createHostConfig(function),
		nil,
		nil,
		containerName,
	)
	if err != nil {
		d.logger.Error("failed to create container", "image", imageRef, "error", err)
		return nil, err
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		d.logger.Error("failed to start container", "id", resp.ID, "error", err)
		_ = d.cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		return nil, err
	}

	address, err := d.resolveAddress(ctx, resp.ID)
	if err != nil {
		_ = d.cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		return nil, err
	}

	d.mu.Lock()
	d.containers[instanceID] = resp.ID
	d.mu.Unlock()

	protocol := function.GetRuntime().GetProtocol()
	if protocol == "" {
		protocol = "grpc"
	}

	return &core.InstanceState{
		InstanceId: instanceID,
		FunctionId: function.GetFunctionId(),
		WorkerId:   req.GetWorkerId(),
		Address:    address,
		Protocol:   protocol,
		Ready:      false,
		StartedAt:  timestamppb.Now(),
	}, nil
}

func (d *Runtime) Stop(ctx context.Context, instanceID uint64) error {
	containerID, err := d.containerID(instanceID)
	if err != nil {
		return err
	}
	stopTimeout := dockerStopTimeoutSeconds(ctx)
	if err := d.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &stopTimeout}); err != nil {
		return err
	}
	statusCh, errCh := d.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case <-statusCh:
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if !d.cfg.AutoRemove {
		if err := d.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
	}

	d.mu.Lock()
	delete(d.containers, instanceID)
	d.mu.Unlock()
	return nil
}

// dockerStopTimeoutSeconds returns a Docker grace period that finishes before
// the caller's deadline. A zero timeout tells Docker to send SIGKILL without a
// grace period when less than the response reserve remains.
func dockerStopTimeoutSeconds(ctx context.Context) int {
	gracePeriod := maxStopGracePeriod
	if deadline, ok := ctx.Deadline(); ok {
		available := time.Until(deadline) - stopCompletionReserve
		if available < gracePeriod {
			gracePeriod = available
		}
	}
	if gracePeriod <= 0 {
		return 0
	}
	return int(gracePeriod / time.Second)
}

func (d *Runtime) Stats(ctx context.Context, instanceID uint64) (*core.ResourceUsage, error) {
	containerID, err := d.containerID(instanceID)
	if err != nil {
		return nil, err
	}

	resp, err := d.cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return &core.ResourceUsage{
		CpuUnits:    payload.CPUStats.CPUUsage.TotalUsage,
		MemoryBytes: payload.MemoryStats.Usage,
	}, nil
}

func (d *Runtime) WatchLifecycle(ctx context.Context, instanceID uint64) (<-chan runtime.LifecycleEvent, error) {
	containerID, err := d.containerID(instanceID)
	if err != nil {
		return nil, err
	}

	out := make(chan runtime.LifecycleEvent, 1)
	go func() {
		defer close(out)
		event, watchErr := d.monitorContainer(ctx, containerID)
		if watchErr != nil && !errors.Is(watchErr, context.Canceled) {
			d.logger.Debug("container monitor finished", "instance_id", instanceID, "error", watchErr)
		}
		select {
		case out <- event:
		case <-ctx.Done():
		}

		d.mu.Lock()
		delete(d.containers, instanceID)
		d.mu.Unlock()
	}()
	return out, nil
}

func (d *Runtime) ensureImage(ctx context.Context, imageRef string) error {
	has, err := d.HasImage(ctx, imageRef)
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	d.logger.Info("pulling image", "image", imageRef)
	reader, err := d.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return status.Error(codes.NotFound, err.Error())
	}
	_, _ = io.Copy(io.Discard, reader)
	_ = reader.Close()
	return nil
}

func (d *Runtime) createContainerConfig(function *core.FunctionSpec, imageRef string, instanceID uint64, controllerAddress string) *container.Config {
	env := []string{
		"CONTROLLER_ADDRESS=" + controllerAddress,
		"INSTANCE_ID=" + strconv.FormatUint(instanceID, 10),
		"FUNCTION_ID=" + strconv.FormatUint(function.GetFunctionId(), 10),
	}
	for key, value := range function.GetRuntime().GetEnv() {
		env = append(env, key+"="+value)
	}

	port := nat.Port(fmt.Sprintf("%d/tcp", functionPort))
	return &container.Config{
		Image: imageRef,
		ExposedPorts: nat.PortSet{
			port: struct{}{},
		},
		Env: env,
		Labels: map[string]string{
			instanceIDLabel: strconv.FormatUint(instanceID, 10),
			functionIDLabel: strconv.FormatUint(function.GetFunctionId(), 10),
		},
	}
}

func (d *Runtime) createHostConfig(function *core.FunctionSpec) *container.HostConfig {
	networkMode := container.NetworkMode(d.cfg.NetworkName)
	if d.cfg.NetworkName == "" {
		networkMode = "bridge"
	}

	resources := container.Resources{}
	if spec := function.GetRuntime().GetResources(); spec != nil {
		if spec.GetMemoryBytes() > 0 {
			resources.Memory = int64(spec.GetMemoryBytes())
		}
		if spec.GetCpuUnits() > 0 {
			resources.CPUPeriod = int64(defaultCPUPeriod)
			quota := int64(defaultCPUPeriod * spec.GetCpuUnits() / 1000)
			if quota < 1000 {
				quota = 1000
			}
			resources.CPUQuota = quota
		}
	}

	return &container.HostConfig{
		AutoRemove:      d.cfg.AutoRemove,
		NetworkMode:     networkMode,
		PublishAllPorts: true,
		Resources:       resources,
	}
}

func (d *Runtime) controllerAddress(ctx context.Context) string {
	gatewayIP := dockerBridgeGatewayIP
	networkName := d.cfg.NetworkName
	if networkName == "" {
		networkName = "bridge"
	}
	if inspected, err := d.cli.NetworkInspect(ctx, networkName, dockernetwork.InspectOptions{}); err == nil {
		for _, cfg := range inspected.IPAM.Config {
			if cfg.Gateway != "" {
				gatewayIP = cfg.Gateway
				break
			}
		}
	} else {
		d.logger.Warn("failed to inspect docker network gateway", "network", networkName, "error", err)
	}

	_, port, err := net.SplitHostPort(d.cfg.WorkerListenAddress)
	if err != nil {
		return gatewayIP + ":50052"
	}
	return gatewayIP + ":" + port
}

func (d *Runtime) resolveAddress(ctx context.Context, containerID string) (string, error) {
	containerJSON, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}

	portKey := fmt.Sprintf("%d/tcp", functionPort)
	ports, ok := containerJSON.NetworkSettings.Ports[nat.Port(portKey)]
	if !ok || len(ports) == 0 {
		return "", errors.New("container not exposed on function port")
	}

	hostPort := ports[0].HostPort
	hostIP := localHostIP()
	if hostIP == "" {
		hostIP = "127.0.0.1"
	}
	return hostIP + ":" + hostPort, nil
}

func (d *Runtime) containerID(instanceID uint64) (string, error) {
	d.mu.RLock()
	id, ok := d.containers[instanceID]
	d.mu.RUnlock()
	if ok {
		return id, nil
	}

	args := filters.NewArgs()
	args.Add("label", instanceIDLabel+"="+strconv.FormatUint(instanceID, 10))
	containers, err := d.cli.ContainerList(context.Background(), container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", status.Errorf(codes.NotFound, "sandbox %d not found", instanceID)
	}
	return containers[0].ID, nil
}

func (d *Runtime) monitorContainer(ctx context.Context, containerID string) (runtime.LifecycleEvent, error) {
	opt := events.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "container", Value: containerID}),
	}
	eventsChan, errChan := d.cli.Events(ctx, opt)

	for {
		select {
		case event := <-eventsChan:
			switch event.Action {
			case events.ActionDie:
				containerJSON, err := d.cli.ContainerInspect(ctx, containerID)
				if err != nil {
					return runtime.LifecycleCrash, err
				}
				if containerJSON.State.ExitCode == 0 {
					return runtime.LifecycleExit, nil
				}
				return runtime.LifecycleCrash, nil
			case events.ActionOOM:
				return runtime.LifecycleOOM, nil
			}
		case <-errChan:
			continue
		case <-ctx.Done():
			return runtime.LifecycleExit, ctx.Err()
		}
	}
}

func sanitizeImageName(imageRef string) string {
	name := imageRef
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func localHostIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	// First pass: look for physical/primary interfaces like eth* or ens*
	for _, iface := range ifaces {
		name := iface.Name
		if len(name) >= 3 && (name[:3] == "eth" || name[:3] == "ens") {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP.String()
					}
				}
			}
		}
	}
	// Second pass: fallback to any non-virtual interface
	for _, iface := range ifaces {
		name := iface.Name
		if len(name) >= 3 && (name[:3] == "tap" || name[:3] == "veth" || name[:3] == "br-") {
			continue
		}
		if len(name) >= 6 && name[:6] == "docker" {
			continue
		}
		if name == "lo" {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	return ""
}
