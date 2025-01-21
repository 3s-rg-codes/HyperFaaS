package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/helpers"
	"log/slog"
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	DURATION = 2 * time.Second
)

var (
	testController *controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
)

var ( //TODO: implement flags, do we need more?
	dockerTolerance         = flag.Duration("dockerTolerance", DURATION, "Tolerance for container start and stop in seconds")
	requestedRuntime        = flag.String("specifyRuntime", helpers.RUNTIME, "for now only docker, is also default")
	controllerServerAddress = flag.String("ServerAdress", helpers.SERVER_ADDRESS, "specify controller server adress")
	autoRemove              = flag.Bool("autoRemove", true, "specify if containers should be removed after stopping")
	CPUPeriod               = flag.Int64("cpuPeriod", 100000, "CPU period")
	CPUQuota                = flag.Int64("cpuQuota", 50000, "CPU quota")
	MemoryLimit             = (*flag.Int64("memoryLimit", 250000000, "Memory limit in MB")) * 1024 * 1024
	environment             = flag.String("environment", helpers.ENVIRONMENT, "specify environment to run")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
)

// image tag array
var imageTags = []string{"hyperfaas-hello:latest", "hyperfaas-crash:latest", "hyperfaas-echo:latest", "hyperfaas-sleep:latest"}

// TestMain calls setup and teardown functions, and runs all other Test Functions
func TestMain(m *testing.M) {
	setup()
	exitVal := m.Run()
	os.Exit(exitVal)
}

// Initializes the containerRuntime and server and sleeps for 5 seconds to ensure the server is up
func setup() {
	fmt.Println("Test Configuration: ")
	flag.Parse()
	flag.VisitAll(func(f *flag.Flag) {
		isDefault := ""
		if f.DefValue == f.Value.String() {
			isDefault = " (USING DEFAULT VALUE)"
		}
		if f.Name[:5] != "test." || f.Name == "update" {
			fmt.Printf("FLAG: %s = %s%s\n", f.Name, f.Value.String(), isDefault)
		}
	})
	fmt.Println()

	switch *requestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(*autoRemove, *environment, slog.Default().With("runtime", "docker")) //did not work otherwise, using container runtime interface
	}

	//Log setup
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	//Controller
	testController = controller.NewController(runtime, slog.Default(), *controllerServerAddress)
	//CallerServer
	go func() {
		testController.StartServer()
	}()

	//sleep for 5 seconds
	time.Sleep(5 * time.Second)

}

func teardown() {
	//testController.StopServer()
}

// Tests a normal container lifecycle: Start, Call, Stop
func TestNormalExecution(t *testing.T) {

	flag.Parse()
	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	testCases := []helpers.ControllerWorkload{
		{
			TestName:          "normal execution of hello image",
			ImageTag:          imageTags[0],
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
			CallPayload:       []byte("TESTPAYLOAD"),
		},
		{
			TestName:          "normal execution of echo image",
			ImageTag:          imageTags[2],
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
			CallPayload:       []byte("Hello World"),
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.TestName, func(t *testing.T) {
			testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

			grpcStatus, ok := status.FromError(err)
			if !ok {
				t.Logf("Container ID: %v", testContainerID)
				t.Fatalf("Start failed: %v", grpcStatus.Code())
			}
			if testContainerID == nil {
				t.Fatalf("Error: %v", "Container ID is nil")
			}
			assert.Equal(t, ContainerExists(testContainerID.Id), true)
			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)

			t.Logf("Start succeded: %v", testContainerID.Id)

			response, err := client.Call(context.Background(), &common.CallRequest{InstanceId: testContainerID, Data: testCase.CallPayload})

			grpcStatus, ok = status.FromError(err)

			if !ok {
				t.Fatalf("Call failed: %v", grpcStatus.Code())
			}

			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)
			if testCase.ExpectsResponse {
				assert.Equal(t, response.Data, testCase.ExpectedResponse)
			}

			t.Logf("Call succeded: %v", response.Data)

			//stop container
			responseContainerID, err := client.Stop(context.Background(), testContainerID)
			grpcStatus, ok = status.FromError(err)
			if !ok {
				t.Fatalf("Stop failed: %v", grpcStatus.Code())
			}
			//TOLERANCE
			time.Sleep(*dockerTolerance)

			assert.Equal(t, ContainerExists(testContainerID.Id), false)
			assert.Equal(t, responseContainerID.Id, testContainerID.Id)

			t.Logf("Stop succeded: %v", responseContainerID.Id)

		})
	}

	t.Cleanup(func() {
		connection.Close()
	})
}

// Tests that a correct error is returned when a non existing function is stopped
func TestStopNonExistingContainer(t *testing.T) {

	flag.Parse()
	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	testCases := []helpers.ControllerWorkload{
		{
			TestName:          "stopping non existing container",
			InstanceID:        "nonExistingContainer",
			ExpectedError:     true,
			ExpectedErrorCode: codes.NotFound,
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.TestName, func(t *testing.T) {
			_, err := client.Stop(context.Background(), &common.InstanceID{Id: testCase.InstanceID})

			grpcStatus, ok := status.FromError(err)

			if !ok {
				t.Fatalf("gRPC Error: %v", grpcStatus.Code())
			}
			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)

			t.Logf("Stopping unknown instance failed successfully: %v ", grpcStatus.Code())

		})
	}

	t.Cleanup(func() {
		connection.Close()
	})
}

// Tests that a correct error is returned when a non-existing function is called
func TestCallNonExistingContainer(t *testing.T) {

	flag.Parse()
	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	testCases := []helpers.ControllerWorkload{
		{
			TestName:          "calling non existing container",
			InstanceID:        "nonExistingContainer",
			ExpectedError:     true,
			ExpectedErrorCode: codes.NotFound,
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.TestName, func(t *testing.T) {
			_, err := client.Call(context.Background(), &common.CallRequest{InstanceId: &common.InstanceID{Id: testCase.InstanceID}, Data: []byte("")})

			grpcStatus, ok := status.FromError(err)

			if !ok {
				t.Fatalf("gRPC Error: %v", grpcStatus.Code())
			}
			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)

			t.Logf("Calling unknown instance failed successfully: %v ", grpcStatus.Code())

		})
	}

	t.Cleanup(func() {
		connection.Close()
	})
}

func TestMetrics(t *testing.T) {
	flag.Parse()
	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)

	metrics, err := client.Metrics(context.Background(), &pb.MetricsRequest{NodeID: "a"})
	grpcStatus, ok := status.FromError(err)

	if !ok {
		t.Fatalf("Getting Metricsupdate failed: %v", grpcStatus.Code())
	}
	t.Logf("successfully got metrics: Percentage RAM used programs:%f%%\n, percentage per cpu: %v", metrics.UsedRamPercent, metrics.CpuPercentPercpu)

	t.Cleanup(func() {
		connection.Close()
	})

}

func TestStartNonLocalImages(t *testing.T) {

	flag.Parse()
	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	testCases := []helpers.ControllerWorkload{
		{
			TestName:          "starting non existing image",
			ImageTag:          "asjkdasjk678132613278hadjskdasjk2314678432768ajbfakjfakhj",
			ExpectedError:     true,
			ExpectedErrorCode: codes.NotFound,
		},
		{
			TestName:          "starting image that needs to be pulled",
			ImageTag:          "luccadibe/hyperfaas-functions:hello",
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.TestName, func(t *testing.T) {

			//Check if the image already exists locally

			opt := image.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "reference", Value: testCase.ImageTag}),
			}

			localImages, err := runtime.Cli.ImageList(context.Background(), opt)

			if err != nil {
				t.Fatalf("Could not list local go: %v", err)
			}

			if len(localImages) > 0 {
				t.Logf("Image already exists locally: %v", testCase.ImageTag)
				//erase image
				_, err := runtime.Cli.ImageRemove(context.Background(), localImages[0].ID, image.RemoveOptions{
					Force: true,
				})
				if err != nil {
					t.Fatalf("Could not remove local image: %v", err)
				}
			}

			_, err = client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

			grpcStatus, ok := status.FromError(err)

			if !ok {
				t.Fatalf("gRPC Error: %v", grpcStatus.Code())
			}

			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)

			if testCase.ExpectedError {
				t.Logf("Starting unknown image failed successfully: %v ", grpcStatus.Code())
			} else {
				t.Logf("Remote image was pulled and started successfully: %v ", testCase.ImageTag)
			}

		})
	}

	t.Cleanup(func() {
		connection.Close()
	})
}

/*
func helpers.BuildMockClient(t *testing.T) (pb.ControllerClient, error) {
	var err error
	connection, err := grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Errorf("Could not start client for testing purposes: %v.", err)
		return nil, err
	}
	t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)
	defer connection.Close()
	return testClient, nil
}

*/

func ContainerExists(instanceID string) bool {
	// Check if the image is present
	_, err := runtime.Cli.ContainerInspect(context.Background(), instanceID)
	return err == nil
}
