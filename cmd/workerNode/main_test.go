package main

import (
	"context"
	"flag"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
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

var ( //TODO: implement flags, do we need more?
	specifyTestType  = flag.String("specifyTestType", "0", "should be Integer, documentation see ReadMe") //TODO: write docu into readme
	requestedRuntime = flag.String("specifyRuntime", "docker", "for now only docker, is also default")
	imageTagFlag     = flag.String("imageTag", "", "specify ImageTag")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
	controllerServerAddress = flag.String("ServerAdress", "", "specify controller server adress")

	testIDs        []string
	testController controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
)

func TestEndToEnd(t *testing.T) {
	buildMockClient(t)

	flag.Parse()
	requestedTest, _ := strconv.Atoi(*specifyTestType)

	switch *requestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime()
	}

	//Controller
	testController = controller.New(runtime)
	//CallerServer
	testController.StartServer()

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

func StartRegularContainer(t *testing.T) {
	if testStartRequest == nil {
		t.Fatalf("Code is dogshit, testStartRequest shouldnt be nil at this point")
	}
	//configure Test
	testContainerID, err := testClient.Start(testCtx, testStartRequest)
	if err != nil {
		t.Fatalf("Starting container w/ start request %v failed. Testing stopped!", testStartRequest)
	}
	testIDs = append(testIDs, testContainerID.Id)
	t.Logf("Container started with ID %v (Ctx: %v, Req: %v)", testContainerID, testCtx, testStartRequest)
}

func TestRegularExecution(t *testing.T) { //TODO
	StartRegularContainer(t)
	runtime.GetCLI().ContainerKill(context.Background())
}

func TestKillDockerDuringExec(t *testing.T) {
	StartRegularContainer(t)
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

/*func TestStart(t *testing.T) {
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

}*/
