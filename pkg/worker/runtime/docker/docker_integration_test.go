//go:build integration

package docker_test

/*
Docker runtime integration tests via the worker gRPC API.

Prerequisites:
  1. Docker daemon running.
  2. Build the echo function image first:
       just build-echo-grpc-image
*/

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/worker"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

const (
	defaultEchoImage     = "echo-grpc:latest"
	defaultStubbornImage = "ubuntu:24.04"
	instanceIDLabel      = "hyperfaas.instance_id"
	testStartTimeout     = 2 * time.Minute
	testStopTimeout      = 30 * time.Second
)

var (
	workerClient workerpb.SandboxServiceClient
	echoImage    string
	workerCancel context.CancelFunc
	nextInstance uint64 = 10_000
)

func TestMain(m *testing.M) {
	if _, err := client.NewClientWithOpts(client.FromEnv); err != nil {
		os.Exit(0)
	}

	echoImage = os.Getenv("HYPERFAAS_ECHO_GRPC_IMAGE")
	if echoImage == "" {
		echoImage = defaultEchoImage
	}
	if !imageExistsLocally(echoImage) {
		os.Stderr.WriteString("image " + echoImage + " not found locally; run: just build-echo-grpc-image\n")
		os.Exit(1)
	}

	port := freePort()
	cfg := worker.WorkerConfig{
		NodeID: "test-worker",
		Logging: config.LoggingConfig{
			Level:  "error",
			Format: "text",
		},
		Server: config.ServerConfig{
			ListenAddress: net.JoinHostPort("0.0.0.0", port),
		},
		Runtime: worker.RuntimeConfig{
			Type: "docker",
			Docker: worker.DockerConfig{
				AutoRemove:  false,
				NetworkName: "bridge",
			},
		},
		Stats: worker.StatsConfig{
			UpdateBufferSize: 1000,
			MetricsInterval:  time.Second,
			BudgetCPU:        2,
			BudgetMemory:     4 * 1024 * 1024 * 1024,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := worker.NewServer(cfg, logger)
	if err != nil {
		os.Stderr.WriteString("NewServer: " + err.Error() + "\n")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	workerCancel = cancel

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	if !waitForWorker(port, 15*time.Second) {
		cancel()
		os.Stderr.WriteString("worker did not become ready on port " + port + "\n")
		os.Exit(1)
	}

	conn, err := grpc.NewClient(
		net.JoinHostPort("127.0.0.1", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		cancel()
		os.Stderr.WriteString("dial worker: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer conn.Close()
	workerClient = workerpb.NewSandboxServiceClient(conn)

	code := m.Run()
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
	}
	os.Exit(code)
}

func TestIntegrationStartCallCleanup(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testStartTimeout)
	defer cancel()

	instanceID := allocInstanceID()
	instance := createSandbox(t, ctx, instanceID)

	payload := []byte("start-call-cleanup")
	callEcho(t, ctx, instance.GetAddress(), payload)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), testStopTimeout)
	defer stopCancel()
	if _, err := workerClient.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: instanceID}); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}

	assertSandboxNotRunning(t, instanceID)
	assertNotListed(t, instanceID)
}

func TestIntegrationStartStopVerifyStopped(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testStartTimeout)
	defer cancel()

	instanceID := allocInstanceID()
	instance := createSandbox(t, ctx, instanceID)

	callEcho(t, ctx, instance.GetAddress(), []byte("before-stop"))
	assertSandboxRunning(t, instanceID)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), testStopTimeout)
	defer stopCancel()
	if _, err := workerClient.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: instanceID}); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}

	assertSandboxNotRunning(t, instanceID)
	assertNotListed(t, instanceID)

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	if err := tryEcho(callCtx, instance.GetAddress(), []byte("after-stop")); err == nil {
		t.Fatal("expected echo call after stop to fail")
	}
}

func TestIntegrationStopNotFound(t *testing.T) {
	requireDocker(t)

	const missingInstanceID = uint64(999_999_999)
	stopCtx, cancel := context.WithTimeout(context.Background(), testStopTimeout)
	defer cancel()

	_, err := workerClient.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: missingInstanceID})
	if err == nil {
		t.Fatal("expected error stopping missing sandbox")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v (%v)", status.Code(err), err)
	}
}

func TestIntegrationStopContainerIgnoringSIGTERMBeforeRPCDeadline(t *testing.T) {
	requireDocker(t)

	stubbornImage := os.Getenv("HYPERFAAS_STUBBORN_IMAGE")
	if stubbornImage == "" {
		stubbornImage = defaultStubbornImage
	}
	if !imageExistsLocally(stubbornImage) {
		t.Skipf("stubborn test image %q is not available locally", stubbornImage)
	}

	instanceID := allocInstanceID()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	defer cli.Close()

	created, err := cli.ContainerCreate(
		context.Background(),
		&container.Config{
			Image: stubbornImage,
			Cmd:   []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"},
			Labels: map[string]string{
				instanceIDLabel: strconv.FormatUint(instanceID, 10),
			},
		},
		nil,
		nil,
		nil,
		"",
	)
	if err != nil {
		t.Fatalf("create stubborn container: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = cli.ContainerRemove(cleanupCtx, created.ID, container.RemoveOptions{Force: true})
	}()
	if err := cli.ContainerStart(context.Background(), created.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start stubborn container: %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	startedAt := time.Now()
	if _, err := workerClient.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: instanceID}); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
	elapsed := time.Since(startedAt)
	if elapsed >= 9*time.Second {
		t.Fatalf("StopSandbox took %v, want less than 9s to leave RPC response time", elapsed)
	}
	assertSandboxNotRunning(t, instanceID)
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := client.NewClientWithOpts(client.FromEnv); err != nil {
		t.Skip("docker daemon is not available")
	}
}

func createSandbox(t *testing.T, ctx context.Context, instanceID uint64) *core.InstanceState {
	t.Helper()

	function := echoFunction(instanceID)
	if _, err := workerClient.PrepareImage(ctx, &workerpb.PrepareImageRequest{Function: function}); err != nil {
		t.Fatalf("PrepareImage: %v", err)
	}

	instance, err := workerClient.CreateSandbox(ctx, &workerpb.CreateSandboxRequest{
		Request: &core.StartSandboxRequest{
			Function:   function,
			WorkerId:   7,
			InstanceId: instanceID,
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if !instance.GetReady() {
		t.Fatal("expected sandbox to be ready")
	}
	if instance.GetAddress() == "" {
		t.Fatal("expected sandbox address")
	}
	return instance
}

func callEcho(t *testing.T, ctx context.Context, address string, payload []byte) {
	t.Helper()
	if err := tryEcho(ctx, address, payload); err != nil {
		t.Fatalf("Echo: %v", err)
	}
}

func tryEcho(ctx context.Context, address string, payload []byte) error {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	echoClient := echopb.NewEchoClient(conn)
	resp, err := echoClient.Echo(ctx, &echopb.EchoRequest{Data: payload})
	if err != nil {
		return err
	}
	if string(resp.GetData()) != string(payload) {
		return status.Errorf(codes.Internal, "echo mismatch: got %q want %q", resp.GetData(), payload)
	}
	return nil
}

func echoFunction(instanceID uint64) *core.FunctionSpec {
	return &core.FunctionSpec{
		UserId:     1,
		FunctionId: instanceID,
		Runtime: &core.RuntimeSpec{
			Image:    echoImage,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 512 * 1024 * 1024,
				CpuUnits:    100000,
			},
		},
	}
}

func assertSandboxRunning(t *testing.T, instanceID uint64) {
	t.Helper()
	running, err := sandboxRunning(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("inspect sandbox: %v", err)
	}
	if !running {
		t.Fatalf("expected sandbox %d to be running", instanceID)
	}
}

func assertSandboxNotRunning(t *testing.T, instanceID uint64) {
	t.Helper()
	running, err := sandboxRunning(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("inspect sandbox: %v", err)
	}
	if running {
		t.Fatalf("expected sandbox %d to be stopped", instanceID)
	}
}

func assertNotListed(t *testing.T, instanceID uint64) {
	t.Helper()
	resp, err := workerClient.ListSandboxes(context.Background(), &workerpb.ListSandboxesRequest{})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	for _, instance := range resp.GetInstances() {
		if instance.GetInstanceId() == instanceID {
			t.Fatalf("sandbox %d still listed by worker", instanceID)
		}
	}
}

func sandboxRunning(ctx context.Context, instanceID uint64) (bool, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return false, err
	}

	args := filters.NewArgs()
	args.Add("label", instanceIDLabel+"="+strconv.FormatUint(instanceID, 10))
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return false, err
	}
	for _, c := range containers {
		if c.State == "running" {
			return true, nil
		}
	}
	return false, nil
}

func imageExistsLocally(imageRef string) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return false
	}
	args := filters.NewArgs()
	args.Add("reference", imageRef)
	images, err := cli.ImageList(context.Background(), image.ListOptions{Filters: args})
	if err != nil {
		return false
	}
	return len(images) > 0
}

func freePort() string {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return strconv.Itoa(port)
}

func waitForWorker(port string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(
			net.JoinHostPort("127.0.0.1", port),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func allocInstanceID() uint64 {
	return atomic.AddUint64(&nextInstance, 1)
}
