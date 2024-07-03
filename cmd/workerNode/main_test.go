package main

import (
	"flag"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

var ( //TODO: implement flags, do we need more?
	specifyTestType  = flag.String("specifyTestType", "0", "should be Integer, documentation see ReadMe") //TODO: write docu into readme
	requestedRuntime = flag.String("specifyRuntime", "docker", "for now only docker, is also default")
	imageTagFlag     = flag.String("imageTag", "", "specify ImageTag")
	passedData       = flag.String("passedData", "", "specify Data to pass to container")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
	controllerServerAddress = flag.String("ServerAdress", "", "specify controller server adress")

	testID         *pb.InstanceID //TODO: for now only one container at a time
	testController controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
)

func TestEndToEnd(t *testing.T) {
	flag.Parse()
	err := buildMockClient(t)
	if err != nil {
		t.Fatalf("Testing stopped! Error: %v", err)
	}

	requestedTest, _ := strconv.Atoi(*specifyTestType)

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

	switch requestedTest {
	case 0:
		TestRegularExecution(t)
		fallthrough
	case 1:
		TestKillDockerNotDuringExec(t)
		fallthrough
	case 2:
		TestKillDockerDuringExec(t)
		fallthrough
	case 3:
		TestKillWorkerDuringExec(t) //needs to be last since we kill the worker and with that all functionality
	//case 4: //call DeployParallel
	//case 5: //call DeployAndKillInstant
	//case 6: //call KillFunctionDuringExec
	default:
		t.Fatalf("Not a valid test type. Testing stopped!")
		return
	}

	err = connection.Close()
	if err != nil {

		t.Errorf("Could not close connection to : %v", err)
	}
	//TODO: what else to pay attention to when ending tests, what else needs to be closed / stopped
}

func StartRegularContainer(t *testing.T) {
	if testStartRequest == nil {
		t.Fatalf("StartRequest not initialized. Testing stopped!") //fatal here since nothing works without startRequest
	}

	testContainerID, err := testClient.Start(testCtx, testStartRequest) //all calls made through test client are our fake client calls
	if err != nil {
		t.Fatalf("Starting container w/ start request %v failed. Testing stopped! Error: %v", testStartRequest, err)
	}

	testID = testContainerID //placeholder line for if we want to run multiple tests in parallel and need to use a slice to store IDs

	t.Logf("Container started with ID %v (Ctx: %v, Req: %v)", testContainerID.Id, testCtx, testStartRequest)
}

// function blocks till answer is returned since testClient.Call blocks till answer is returned
func TestCallContainer(t *testing.T) {
	//t.Logf("Attempting to call container with ID %v and Data %s", testID.Id, determinePassedData(t))
	testCallRequest := &pb.CallRequest{InstanceId: &pb.InstanceID{Id: testID.Id}, Params: &pb.Params{Data: determinePassedData(t)}}

	response, err := testClient.Call(testCtx, testCallRequest)
	if err != nil {
		t.Errorf("Calling container with ID %v failed", testID.Id)
		return
	}

	t.Logf("Calling successful. Response from container with ID %v: %v", testID.Id, response)
}

func ForceKillAndRemoveContainer(t *testing.T) {
	err := runtime.Cli.ContainerKill(testCtx, testID.Id, "SIGKILL")

	//a bit hard to tell what's happening, basically: we try to kill container first with ContainerKill, if that fails we kill/remove it with ContainerRemove
	if err != nil {
		err = runtime.Cli.ContainerRemove(testCtx, testID.Id, container.RemoveOptions{Force: true})
		if err != nil {
			t.Errorf("Killing container with ID %v failed.", testID.Id)
		} else {
			t.Errorf("Container with ID %v killed and removed but not with DockerRemove not DockerKill", testID.Id)
			//not necessarily done with SIGKILL but with SIGTERM which lets container die gracefully --> defeats purpose here
		}
	} else {
		err = runtime.Cli.ContainerRemove(testCtx, testID.Id, container.RemoveOptions{})
		t.Logf("Container with ID %v killed and removed", testID.Id)
	}
}

func TestRegularExecution(t *testing.T) { //TODO

	StartRegularContainer(t)

	TestCallContainer(t)

	t.Logf("Attempting to stop container with ID %v", testID.Id)
	stoppedContainerID, err := testClient.Stop(testCtx, testID)

	if err != nil {
		t.Fatalf("Stopping container with ID %v failed. Testing stopped!", testID.Id) //Fatal because normal execution should work in any case, testing doesn't make sense if it doesn't
		return
	}
	t.Logf("Container with ID %v stopped", stoppedContainerID.Id)
}

func TestKillDockerNotDuringExec(t *testing.T) {
	t.Logf("Starting test for killing container (not during direct execution) with ID %v", testID.Id)

	StartRegularContainer(t)

	//after this returns, container is idling
	TestCallContainer(t)

	ForceKillAndRemoveContainer(t)
}

func TestKillDockerDuringExec(t *testing.T) { //idea here: we kill the docker container while it is executing by deploying goroutine
	t.Logf("Starting test for killing container during execution with ID %v", testID.Id)

	StartRegularContainer(t)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		t.Logf("Goroutine started for killing container with ID %v", testID.Id)

		time.Sleep(5 * time.Second) //wait for container to start execution
		//TODO: test for different times and different functions since it may change behaviour
		ForceKillAndRemoveContainer(t)
	}()

	t.Logf("Starting function call which should be interrupted by goroutine")

	TestCallContainer(t)

	wg.Wait() //wait for TestCallContainer and goroutine to finish
	stoppedContainerID, err := runtime.Stop(testCtx, testID)

	if err == nil {
		t.Errorf("Container with ID %v should have been killed and removed, but wasnt", stoppedContainerID.Id)

	} else {
		t.Logf("Container with ID %v killed and removed, Runtime/Stop returned error %v", testID.Id, err)
	}
}

func TestKillWorkerDuringExec(t *testing.T) {
	t.Logf("Starting test for killing worker during execution")

	StartRegularContainer(t)

	TestCallContainer(t)

	go func() {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Errorf("Could not find process %v", os.Getpid())
		}

		//TODO improve logging here when we get to later stages, right now if the program crashes it indicates killing the container worked
		err = p.Signal(syscall.SIGKILL) //Killing the current process (worker)
		if err != nil {
			t.Errorf("Could not kill process %v", os.Getpid())
		}
	}()
}

func TestDeploySequentially(t *testing.T) {

}

func TestDeployParallel(t *testing.T) {

}

func TestDeployAndKillInstant(t *testing.T) {

}

func TestKillFunctionDuringExec(t *testing.T) {

}
