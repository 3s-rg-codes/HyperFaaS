package main

import (
	"context"
	"flag"
	"fmt"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"strconv"
	"testing"
	"time"
)

type TestType struct {
	id                   int
	regularExec          bool
	killDocker           time.Duration
	killWorker           time.Duration
	deploySequentially   bool
	deployParallel       bool
	deployAndKillInstant bool
	killFunction         time.Duration
} //TODO: kind of this should set the flags so user doesnt have to do it manually

const ( //TODO: how do we create different kinds of the same test e.g. killing container at different times during exec
	RegularExecution int = iota
	KillDockerDuringExec
	KillWorkerDuringExec
	DeploySequentially
	DeployParallel
	DeployAndKillInstant
	KillFunctionDuringExec
)

const AllExecutions int = 1337

type TestConfig struct {
	ctx              context.Context
	req              *pb.StartRequest
	controller       controller.Controller
	controllerAdress string
}

func (t TestConfig) String() string {
	return fmt.Sprintf("context: %v, request: %v, server: %v, testingScenario: %v", t.ctx, t.req, t.controller)
}

var ( //TODO: implement flags, do we need more?
	specifyTestType = flag.String("specifyTestType", "0", "should be Integer, documentation see ReadMe") //TODO: write docu into readme
	testIDs         []string
	testController  controller.Controller
)

func TestEndToEnd(t *testing.T) {
	flag.Parse()
	requestedTest, _ := strconv.Atoi(*specifyTestType)
	dockerR := dockerRuntime.NewDockerRuntime()
	testController = controller.New(dockerR)
	testController.StartServer() //TODO: hwo do i get access to the actual grpc server

	switch requestedTest {
	case 0:
		TestRegularExecution(t) //call TestRegularExecution
	case 1: //call TestKillDocker
	case 2: //call TestKillWorker
	case 3: //call DeploySequentially
	case 4: //call DeployParallel
	case 5: //call DeployAndKillInstant
	case 6: //call KillFunctionDuringExec
	}
}

func TestRegularExecution(t *testing.T) {

	startRequest := &pb.StartRequest{ //TODO: where do these infos come from? client? postman?
		ImageTag: nil,
		Config:   nil,
	}

	regularTestConfig := TestConfig{
		ctx:              context.Background(),
		req:              startRequest,
		controller:       testController,
		controllerAdress: "50051",
	}

	connection, err := grpc.NewClient(regularTestConfig.controllerAdress, grpc.WithTransportCredentials(insecure.NewCredentials()))

	id, err := regularTestConfig.controller.Start(regularTestConfig.ctx, regularTestConfig.req)
	if err != nil {
		t.Fatalf("Failed to start test container: %v", err)
		return
	}
	t.Logf("Started function with id %v on server %v", id, testController)
}

func TestKillDockerDuringExec(t *testing.T) {

}

func TestKillWorkerDuringExec(t *testing.T) {

}

func TestDeploySequentially(t *testing.T) {

}

func TestDeployParallel(t *testing.T) {

}

func TestDeployAndKillInstant(t *testing.T) {

}

func TestKillFunctionDuringExec(t *testing.T) {

}

func TestStart(t *testing.T) {
	flag.Parse()
	requestedTest, _ := strconv.Atoi(*specifyTestType)

	dockerR := dockerRuntime.NewDockerRuntime()
	testController := controller.New(dockerR)
	if requestedTest == 1337 { //run all tests
		testConfigs := make([]*TestConfig, 0, 7) //TODO: how many test scenarios, resize accordingly
		for i := 0; i < len(testConfigs); i++ {
			testConfigs[i] = &TestConfig{ //where do we get the values from
				ctx:             context.Background(),
				req:             &pb.StartRequest{},
				controller:      testController, //TODO: do all tests run on the same server
				testingScenario: TestType(requestedTest),
			}
		}
		t.Logf("configurations for all tests ready in slice: %v", testConfigs) //TODO: where to log to
		t.Logf("Starting tests")
		for _, config := range testConfigs {
			start, err := config.controller.Start(config.ctx, config.req)
			if err != nil {
				//TODO: implement logging
			}
			testIDs = append(testIDs, start.Id)
		}
	} else {
		testConfig := &TestConfig{
			ctx:             context.Background(),
			req:             &pb.StartRequest{},
			server:          *testServer,
			testingScenario: TestType(requestedTest),
		}
		t.Logf("configuration for test %v ready: %s", requestedTest, testConfig.String())
	}

}
