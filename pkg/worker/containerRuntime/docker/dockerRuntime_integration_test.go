//go:build integration

package dockerRuntime

/*This test requires you to have a docker daemon running and the following hyperfaas images to be built:
- hyperfaas-hello:latest
- hyperfaas-echo:latest
- hyperfaas-crash:latest
Also this only tests in non-containerized mode.

To correctly test our docker runtime, we actually need to have an instance of the worker server running, because when the containers start, they immediatly try to call the SignalReady endpoint, which requires the worker server to be running. If that fails, they crash immediately.
*/

import (
	"context"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"math/rand"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	functionpb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	DEFAULT_CONFIG = common.Config{
		Memory: 1024 * 1024 * 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}
	WORKER_ADDRESS = "localhost:50051"
	// The address used by the worker server to listen for connections
	// We use 0.0.0.0 so containers can connect to it
	WORKER_LISTENER_ADDRESS = "0.0.0.0:50051"
)

var once sync.Once

type fakeMetadata struct{}

func (fakeMetadata) GetFunction(ctx context.Context, id string) (*metadata.FunctionMetadata, error) {
	return nil, metadata.ErrFunctionNotFound
}

func startWorkerServer() (chan bool, context.CancelFunc) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dr := getDockerRuntime()
	statsManager := stats.NewStatsManager(logger, time.Duration(10)*time.Second, 1.0, 100000)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		once.Do(func() {
			c := controller.NewController(dr,
				statsManager,
				logger,
				WORKER_LISTENER_ADDRESS,
				fakeMetadata{},
				controller.NewReadySignals(false))
			c.StartServer(ctx)
		})
	}()

	readyChan := make(chan bool)
	go func() {
		for range time.NewTicker(1 * time.Second).C {
			conn, err := grpc.NewClient(WORKER_ADDRESS, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				continue
			}
			conn.Connect()
			defer conn.Close()
			_ = workerpb.NewWorkerClient(conn) // just to check if the connection is successful
			readyChan <- true
			break
		}
	}()
	return readyChan, cancel
}

func getDockerRuntime() *DockerRuntime {
	// check if docker daemon is running
	if _, err := client.NewClientWithOpts(client.FromEnv); err != nil {
		panic("Docker daemon is not running. It is required for this test to run.")
	}
	return NewDockerRuntime(false,
		true,
		WORKER_ADDRESS,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
		"worker",
		"",
	)
}

func pingContainer(ctx context.Context, ip string) error {
	//TODO
	conn, err := grpc.NewClient(ip, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := functionpb.NewFunctionServiceClient(conn)
	_, err = client.Call(ctx, &common.CallRequest{
		Data: []byte("Hello, World!"),
	})
	if err != nil {
		return err
	}

	return nil
}

func getRandId() string {
	return strconv.FormatUint(rand.Uint64(), 10)[:15]
}

func TestMain(m *testing.M) {
	readyChan, cancel := startWorkerServer()
	defer cancel()
	<-readyChan
	os.Exit(m.Run())
}

func TestNewDockerRuntime_Integration(t *testing.T) {
	runtime := getDockerRuntime()

	if runtime == nil {
		t.Fatal("Runtime is nil")
	}

}

func TestDockerRuntime_Start_Integration(t *testing.T) {
	runtime := getDockerRuntime()

	t.Run("should start a container for hyperfaas-hello:latest", func(t *testing.T) {
		container, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-hello:latest", &DEFAULT_CONFIG,
		)

		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}

		if container.Id == "" {
			t.Errorf("Container ID is empty")
		}

		if container.InternalIP == "" {
			t.Errorf("Container IP is empty")
		}

		if container.ExternalIP == "" {
			t.Errorf("Container external IP is empty")
		}

		// Check if container.IP looks like a valid IP:port address, and the IP part is a valid IP
		checkIP(t, container.InternalIP)
		checkIP(t, container.ExternalIP)

		exists := runtime.ContainerExists(context.Background(), container.Id)

		if !exists {
			t.Errorf("Container does not exist")
		}

		t.Run("should report that the container exists", func(t *testing.T) {
			exists := runtime.ContainerExists(context.Background(), container.Id)
			if !exists {
				t.Errorf("Container does not exist")
			}
		})

		t.Run("the created container should be callable", func(t *testing.T) {
			err := pingContainer(context.Background(), container.InternalIP)
			if err != nil {
				t.Errorf("Error pinging container: %v", err)
			}
		})
	})

	t.Run("should fail for a non existing image", func(t *testing.T) {
		_, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-non-existing-image", &DEFAULT_CONFIG,
		)

		if err == nil {
			t.Errorf("Expected error starting container for non existing image")
		}
	})

	t.Run("should be able to start 2 containers simultaneously", func(t *testing.T) {
		var err error
		container1, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-hello:latest", &DEFAULT_CONFIG,
		)

		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}

		container2, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-hello:latest", &DEFAULT_CONFIG,
		)
		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}
		if container1.InternalIP == container2.InternalIP {
			t.Errorf("Container 1 and container 2 should have different IPs, first: %s, second: %s", container1.InternalIP, container2.InternalIP)
		}
		err = pingContainer(context.Background(), container1.InternalIP)
		if err != nil {
			t.Errorf("Error pinging container 1: %v", err)
		}
		err = pingContainer(context.Background(), container2.InternalIP)
		if err != nil {
			t.Errorf("Error pinging container 2: %v", err)
		}
	})

	t.Run("should be able to start a container with a custom timeout", func(t *testing.T) {
		cfg := &common.Config{
			Timeout: 2,
			Memory:  1024 * 1024 * 1024,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  100000,
			},
		}
		container, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-hello:latest", cfg,
		)
		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}
		<-time.After(3 * time.Second)
		exists := runtime.ContainerExists(context.Background(), container.Id)
		if exists {
			t.Errorf("Container still exists after timeout")
		}
	})

}

func TestDockerRuntime_Stop_Integration(t *testing.T) {
	runtime := getDockerRuntime()

	t.Run("should stop a running container", func(t *testing.T) {
		container, err := runtime.Start(
			context.Background(),
			getRandId(), "hyperfaas-hello:latest", &DEFAULT_CONFIG,
		)

		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}

		err = runtime.Stop(context.Background(), container.Id)

		if err != nil {
			t.Errorf("Error stopping container: %v", err)

		}

		t.Run("should report that the container is stopped", func(t *testing.T) {
			exists := runtime.ContainerExists(context.Background(), container.Id)
			if exists {
				t.Errorf("Runtime says container still exists after stop")
			}
		})

		// Check if container is stopped (not running)
		containerJSON, err := runtime.Cli.ContainerInspect(context.Background(), container.Id)
		if err != nil {
			t.Errorf("Error inspecting container: %v", err)
		}

		if containerJSON.State.Running {
			t.Errorf("Container is still running after stop")
		}

		allowed := []string{"exited", "removing", "removed", "dead"}
		if !slices.Contains(allowed, containerJSON.State.Status) {
			t.Errorf("Container status is %s, expected one of %v", containerJSON.State.Status, allowed)
		}
	})

	t.Run("should return an error when trying to stop a non existing container", func(t *testing.T) {
		err := runtime.Stop(context.Background(), "non-existing-container")
		if err == nil {
			t.Errorf("Expected error stopping non existing container")
		}
	})

}

func TestDockerRuntime_MonitorContainer_Integration(t *testing.T) {
	runtime := getDockerRuntime()

	// config for fast timeout
	cfg := &common.Config{
		Timeout: 2,
		Memory:  1024 * 1024 * 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}

	t.Run("should return a timeout event when the container times out", func(t *testing.T) {
		fId := getRandId()
		c, err := runtime.Start(context.Background(), fId, "hyperfaas-hello:latest", cfg)
		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}

		// send a request to the container (makes it crash)
		go func() {
			time.Sleep(1 * time.Second)
			pingContainer(context.Background(), c.InternalIP)
		}()

		event, err := runtime.MonitorContainer(context.Background(), c.Id, fId)
		if err != nil {
			t.Errorf("Error monitoring container: %v", err)
		}
		if event != cr.ContainerEventTimeout {
			t.Errorf("Expected timeout event, got %v", event)
		}
	})

	t.Run("should return a crash event when the container crashes", func(t *testing.T) {
		fId := getRandId()
		c, err := runtime.Start(context.Background(), fId, "hyperfaas-crash:latest", cfg)
		if err != nil {
			t.Errorf("Error starting container: %v", err)
		}

		// send a request to the container (makes it crash)
		go func() {
			time.Sleep(1 * time.Second)
			pingContainer(context.Background(), c.InternalIP)
		}()

		event, err := runtime.MonitorContainer(context.Background(), c.Id, fId)

		if err != nil {
			t.Errorf("Error monitoring container: %v", err)
		}
		if event != cr.ContainerEventCrash {
			t.Errorf("Expected crash event, got %v", event)
		}
	})
}

// checks if the ip is a valid IP:port address
func checkIP(t *testing.T, ip string) {
	host, port, err := net.SplitHostPort(ip)
	if err != nil {
		t.Errorf("IP is not a valid IP:port address: %v", ip)
	}
	ipParsed := net.ParseIP(host)
	if ipParsed == nil || host == "" {
		t.Errorf("IP does not contain a valid IP address: %v", ip)
	}
	if port == "" {
		t.Errorf("IP does not contain a port: %v", ip)
	}
}
