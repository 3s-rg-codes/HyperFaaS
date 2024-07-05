package main

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ( //TODO: implement flags, do we need more?
	specifyTestType  = flag.String("specifyTestType", "0", "should be Integer, documentation see ReadMe") //TODO: write docu into readme
	requestedRuntime = flag.String("specifyRuntime", "docker", "for now only docker, is also default")
	passedData       = flag.String("passedData", "", "specify Data to pass to container")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
	controllerServerAddress = flag.String("ServerAdress", "localhost:50051", "specify controller server adress")

	testID         *pb.InstanceID //TODO: for now only one container at a time
	testController controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
)

// image tag array
var imageTags = []string{"hello:latest", "crash:latest", "echo:latest", "sleep:latest"}

type testCase struct {
	testName          string
	ImageTag          string
	ExpectedError     bool
	ReturnError       bool
	ExpectsResponse   bool
	ExpectedResponse  string
	ErrorCode         codes.Code
	ExpectedErrorCode codes.Code
	CallPayload       string
	InstanceID        string
}

// TestMain calls setup and teardown functions, and runs all other Test Functions
func TestMain(m *testing.M) {
	setup()
	exitVal := m.Run()
	os.Exit(exitVal)
}

// Initializes the containerRuntime and server and sleeps for 5 seconds to ensure the server is up
func setup() {

	flag.Parse()
	switch *requestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime() //did not work otherwise, using container runtime interface
	}

	//Controller
	testController = controller.New(runtime)
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
	client, connection := BuildMockClient(t)
	testCases := []testCase{
		{
			testName:          "normal execution of hello image",
			ImageTag:          imageTags[0],
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "",
		},
		{
			testName:          "normal execution of echo image",
			ImageTag:          imageTags[2],
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "Hello World",
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.testName, func(t *testing.T) {
			testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{}})
			grpcStatus, ok := status.FromError(err)
			if !ok {
				t.Logf("Container ID: %v", testContainerID)
				t.Fatalf("Start failed: %v", grpcStatus.Code())
			}
			assert.Equal(t, grpcStatus.Code(), testCase.ExpectedErrorCode)

			if testContainerID == nil {
				t.Fatalf("Error: %v", "Container ID is nil")
			}
			t.Logf("Start succeded: %v", testContainerID.Id)

			response, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: testContainerID, Params: &pb.Params{Data: testCase.CallPayload}})

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
	client, connection := BuildMockClient(t)
	testCases := []testCase{
		{
			testName:          "stopping non existing container",
			InstanceID:        "nonExistingContainer",
			ExpectedError:     true,
			ExpectedErrorCode: codes.NotFound,
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.testName, func(t *testing.T) {
			_, err := client.Stop(context.Background(), &pb.InstanceID{Id: testCase.InstanceID})

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

// Tests that a correct error is returned when a non existing function is called
func TestCallNonExistingContainer(t *testing.T) {

	flag.Parse()
	client, connection := BuildMockClient(t)
	testCases := []testCase{
		{
			testName:          "calling non existing container",
			InstanceID:        "nonExistingContainer",
			ExpectedError:     true,
			ExpectedErrorCode: codes.NotFound,
		},
	}

	for _, testCase := range testCases {

		t.Run(testCase.testName, func(t *testing.T) {
			_, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: &pb.InstanceID{Id: testCase.InstanceID}, Params: &pb.Params{}})

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

/*
func BuildMockClient(t *testing.T) (pb.ControllerClient, error) {
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
