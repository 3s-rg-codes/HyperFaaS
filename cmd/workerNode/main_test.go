package main

import (
	"flag"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"strconv"
	"sync"
	"testing"
	"time"
)

var ( //TODO: implement flags, do we need more?
	specifyTestType  = flag.String("specifyTestType", "0", "should be Integer, documentation see ReadMe") //TODO: write docu into readme
	requestedRuntime = flag.String("specifyRuntime", "docker", "for now only docker, is also default")
	imageTagFlag     = flag.String("imageTag", "", "specify ImageTag")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
	controllerServerAddress = flag.String("ServerAdress", "", "specify controller server adress")

	testIDs        []*pb.InstanceID
	testController controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
)

func TestEndToEnd(t *testing.T) {
	flag.Parse()
	buildMockClient(t)

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

	err := connection.Close()
	if err != nil {
		t.Errorf("Could not close connection to : %v", err)
	}
	//TODO: what else to pay attention to when ending tests, what else needs to be closed / stopped
}

func StartRegularContainer(t *testing.T) {
	if testStartRequest == nil {
		t.Errorf("Code is dogshit, testStartRequest shouldnt be nil at this point")
	}
	//configure Test
	testContainerID, err := testClient.Start(testCtx, testStartRequest)
	if err != nil {
		t.Errorf("Starting container w/ start request %v failed. Testing stopped!", testStartRequest)
	}
	testIDs = append(testIDs, testContainerID)
	t.Logf("Container started with ID %v (Ctx: %v, Req: %v)", testContainerID.Id, testCtx, testStartRequest)
}

func TestCallContainer(t *testing.T) {
	t.Logf("Attempting to call container with ID %v", testIDs[0].Id)
	testCallRequest := &pb.CallRequest{Params: &pb.Params{Data: ""}}
	response, err := testClient.Call(testCtx, testCallRequest)
	if err != nil {
		t.Errorf("Calling container with ID %v failed. Testing stopped!", testIDs[0].Id)
	}
	t.Logf("Calling successful. Response from container with ID %v: %v", testIDs[0].Id, response)
}

func ForceKillAndRemoveContainer(t *testing.T) {
	err := runtime.GetCLI().ContainerKill(testCtx, testIDs[0].Id, "SIGKILL")
	if err != nil {
		err = runtime.GetCLI().ContainerRemove(testCtx, testIDs[0].Id, container.RemoveOptions{Force: true})
		if err != nil {
			t.Errorf("Killing container with ID %v failed. Testing stopped!", testIDs[0].Id)
		} else {
			t.Logf("Container with ID %v killed", testIDs[0].Id)
		}
	} else {
		err = runtime.GetCLI().ContainerRemove(testCtx, testIDs[0].Id, container.RemoveOptions{})
		t.Logf("Container with ID %v killed and removed", testIDs[0].Id)
	}
}

func TestRegularExecution(t *testing.T) { //TODO
	t.Logf("Starting regular execution test for container with ID %v", testIDs[0].Id)
	//Start container
	StartRegularContainer(t)

	//call container
	TestCallContainer(t)

	//stop container
	t.Logf("Attempting to stop container with ID %v", testIDs[0].Id)
	stoppedContainerID, err := testClient.Stop(testCtx, testIDs[0])
	if err != nil {
		t.Errorf("Stopping container with ID %v failed. Testing stopped!", testIDs[0].Id)
	}
	t.Logf("Container with ID %v stopped", stoppedContainerID.Id)
}

func TestKillDockerNotDuringExec(t *testing.T) {
	StartRegularContainer(t)
	//call container to ensure it is running/working
	TestCallContainer(t)
	ForceKillAndRemoveContainer(t)
}

func TestKillDockerDuringExec(t *testing.T) { //idea here: we kill the docker container while it is executing by deploying goroutine
	StartRegularContainer(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Second) //wait for container to start execution
		//TODO: test for different times and different functions since it may change behaviour
		ForceKillAndRemoveContainer(t)
	}()
	TestCallContainer(t)
	wg.Wait()
	stoppedContainerID, err := runtime.Stop(testCtx, testIDs[0])
	if err == nil {
		t.Errorf("Container with ID %v should have been killed and removed, but wasnt", stoppedContainerID.Id)
	}
	t.Logf("Container with ID %v killed and removed, Runtime/Stop returned error %v", testIDs[0].Id, err)
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
