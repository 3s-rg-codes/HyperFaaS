package main

import (
	"context"
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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
	controllerServerAddress = flag.String("ServerAdress", "", "specify controller server adress")

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

func TestMain(t *testing.T) {
	setup()

	TestNormalExecution(t)

}

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

func TestNormalExecution(t *testing.T) {

	flag.Parse()
	client, err := BuildMockClient(t)
	if err != nil {
		t.Fatalf("Testing stopped! Error: %v", err)
	}

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

			status, ok := status.FromError(err)
			if !ok {
				t.Fatalf("Start failed: %v", status.Code())
			}
			assert.Equal(t, status.Code(), testCase.ExpectedErrorCode)

			if testContainerID == nil {
				t.Fatalf("Error: %v", "Container ID is nil")
			}
			t.Logf("Start succeded: %v", testContainerID.Id)

			response, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: testContainerID, Params: &pb.Params{Data: testCase.CallPayload}})

			status, ok = status.FromError(err)

			if !ok {
				t.Fatalf("Call failed: %v", status.Code())
			}

			assert.Equal(t, status.Code(), testCase.ExpectedErrorCode)
			if testCase.ExpectsResponse {
				assert.Equal(t, response.Data, testCase.ExpectedResponse)
			}

			t.Logf("Call succeded: %v", response.Data)

			//stop container

			responseContainerID, err := client.Stop(context.Background(), testContainerID)

			status, ok = status.FromError(err)

			if !ok {
				t.Fatalf("Stop failed: %v", status.Code())
			}
			assert.Equal(t, &responseContainerID, testContainerID)

			t.Logf("Stop succeded: %v", responseContainerID.Id)

		})
	}

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
