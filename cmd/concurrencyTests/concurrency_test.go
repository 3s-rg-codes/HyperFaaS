package concurrencyTests

import (
	"context"
	"flag"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/caller"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/mockRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gotest.tools/v3/assert"
	"os"
	"sync"
	"testing"
	"time"
)

const (
	DURATION        = 2 * time.Second
	TIMEOUT         = 40
	RUNTIME         = "docker"
	SERVER_ADDRESS  = "localhost:50051"
	CONTAINER_COUNT = 10
)

var (
	dockerTolerance  = flag.Duration("dockerTolerance", DURATION, "Tolerance for container start and stop in seconds")
	requestedRuntime = flag.String("specifyRuntime", RUNTIME, "for now only docker, is also default")
	//config                  = flag.String("config", "", "specify Config") TODO WIP, not implemented yet(?)
	controllerServerAddress = flag.String("ServerAdress", SERVER_ADDRESS, "specify controller server adress")
	autoRemove              = flag.Bool("autoRemove", true, "specify if containers should be removed after stopping")
	containerCount          = flag.Int("containerCount", CONTAINER_COUNT, "Number of containers to be created")
	timeout                 = flag.Duration("timeout", TIMEOUT*time.Second, "Timeout for waiting for container response")
)

var (
	testController controller.Controller
	runtime        *dockerRuntime.DockerRuntime //TODO generalize for all, problem: cant access fields of dockerruntime if of type containerruntime
	fakeRuntime    *mockRuntime.FakeRuntime
	imageTags      = []string{"hyperfaas-hello:latest", "hyperfaas-crash:latest", "hyperfaas-echo:latest", "hyperfaas-sleep:latest"}
	containerMap   map[int]*pb.InstanceID
)

var (
	mutex    sync.RWMutex
	swgStart SafeWaitGroup
	swgCall  SafeWaitGroup
	swgStop  SafeWaitGroup
)

func TestMain(m *testing.M) {
	setup()
	exitVal := m.Run()
	os.Exit(exitVal)
}

type controllerWorkload struct {
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

type concurrencyStatistics struct {
	startAttempts       int
	callAttempts        int
	stopAttempts        int
	successfullyStarted int
	successfullyCalled  int
	successfullyStopped int
}

func setup() {
	fmt.Println("Concurrency Test Configuration")
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
		cs := caller.New()
		sm := stats.New()

		go cs.Start()
		go sm.StartStreamingToListeners()

		var err error
		runtime, err = dockerRuntime.NewDockerRuntime(*autoRemove, &cs, &sm) //did not work otherwise, using container runtime interface
		if err != nil {
			log.Fatal().Msgf("FATAL: Could not start docker runtime: %v", err)
			return
		}
	case "mockRuntime":
		fakeRuntime = mockRuntime.NewFakeRuntime(2)
	default:
		log.Fatal().Msgf("FATAL: Invalid Runtime")
	}

	//Log setup
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel).With().Caller().Logger()

	//Controller
	switch *requestedRuntime {
	case "docker":
		testController = controller.New(runtime)

	case "mockRuntime":
		testController = controller.New(fakeRuntime)
	}
	//CallerServer
	go func() {
		testController.StartServer()
	}()

	//sleep for 5 seconds
	time.Sleep(5 * time.Second)
	containerMap = make(map[int]*pb.InstanceID)
}

func TestConcurrencyStartAndStop(t *testing.T) {
	flag.Parse()
	client, connection := BuildMockClient(t)
	statistics := concurrencyStatistics{}
	testCases := []controllerWorkload{
		{
			testName:          "normal execution of hello image",
			ImageTag:          imageTags[0],
			ExpectedError:     false,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "test",
			ExpectedResponse:  "HELLO WORLD!",
		},
		{
			testName:          "normal execution of echo image",
			ImageTag:          imageTags[2],
			ExpectedError:     false,
			ExpectsResponse:   true,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "Hello World",
			ExpectedResponse:  "Hello World",
		},
	}

	swgStart.Add(*containerCount)

	for i := 0; i < *containerCount; i++ {
		i := i
		go func() {
			//____________________________Starting________________________________
			testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCases[i%2].ImageTag}, Config: &pb.Config{}})
			mutex.Lock()
			statistics.startAttempts++
			mutex.Unlock()

			grpcStatus, ok := status.FromError(err)
			if !ok {
				t.Logf("ContainerID: %v. Start failed: %v", testContainerID.Id, err)
				return
			}
			if testContainerID == nil {
				t.Logf("Start failed: %v", grpcStatus.Code())
				return
			}

			assert.Equal(t, ContainerExists(testContainerID.Id), true)
			assert.Equal(t, grpcStatus.Code(), testCases[i%2].ExpectedErrorCode)

			t.Logf("Start succeded: %v", testContainerID.Id)
			mutex.Lock()
			containerMap[i] = testContainerID
			statistics.successfullyStarted++
			mutex.Unlock()
			swgStart.Done()
		}()
	}
	t.Logf("Waiting for containers to start")

	/*
		if !waitWithTimeout(&swg, *timeout) {
			t.Logf("Timeout reached, now calling containers")
		}
	*/
	time.Sleep(*timeout)
	t.Logf("Timeout reached, now calling containers")
	swgCall.Add(*containerCount)

	for i := 0; i < *containerCount; i++ {
		i := i
		go func() {
			mutex.Lock()
			testContainerID, contains := containerMap[i]
			statistics.callAttempts++
			mutex.Unlock()
			if !contains {
				t.Logf("Container not found in map")
				return
			}
			//fmt.Println(testContainerID.Id)
			//____________________________Calling________________________________
			response, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: testContainerID, Params: &pb.Params{Data: testCases[i%2].CallPayload}})

			grpcStatus, ok := status.FromError(err)

			if !ok {
				t.Logf("Call failed: %v", grpcStatus.Code())
			}

			assert.Equal(t, grpcStatus.Code(), testCases[i%2].ExpectedErrorCode)
			//No error expected here so no need to check if its the expected error
			assert.Equal(t, testCases[i%2].ExpectedResponse, response.Data)

			t.Logf("Call succeded: %v", response.Data)
			mutex.Lock()
			statistics.successfullyCalled++
			mutex.Unlock()
			swgCall.Done()
		}()
	}
	t.Logf("Waiting for containers to call")

	/*
		if !waitWithTimeout(&swg, *timeout) {
			t.Logf("Timeout reached, now stopping containers")
		}

	*/
	time.Sleep(*timeout)
	t.Logf("Timeout reached, now stopping containers")
	swgStop.Add(*containerCount)

	for i := 0; i < *containerCount; i++ {
		i := i
		go func() {
			fmt.Println()
			mutex.Lock()
			testContainerID, contains := containerMap[i]
			statistics.stopAttempts++
			mutex.Unlock()
			if !contains {
				t.Logf("Container not found in map")
				return
			}
			//____________________________Stopping________________________________
			responseContainerID, err := client.Stop(context.Background(), testContainerID)
			grpcStatus, ok := status.FromError(err)
			if !ok {
				t.Logf("Stop failed: %v", grpcStatus.Code())
				return
			}
			//TOLERANCE
			time.Sleep(*dockerTolerance) //TODO: should this be zero if we arent using docker?

			assert.Equal(t, ContainerExists(testContainerID.Id), false)
			assert.Equal(t, responseContainerID.Id, testContainerID.Id)

			t.Logf("Stop succeded: %v", responseContainerID.Id)
			mutex.Lock()
			delete(containerMap, i)
			statistics.successfullyStopped++
			mutex.Unlock()
			swgStop.Done()
		}()
	}
	t.Logf("Waiting for containers to stop")

	time.Sleep(*timeout)
	t.Logf("Timeout reached, now evaluating test")
	//____________________________Cleanup________________________________
	//wait for all Goroutines to finish with Waitgroup
	/*
		if !waitWithTimeout(&swg, *timeout) {
			t.Logf("Timeout reached, now evaluating test")
		}

	*/

	t.Cleanup(func() {
		err := connection.Close()
		if err != nil {
			t.Logf("Could not close connection: %v", err)
		}
		evaluateStatistics(statistics)
		assert.Equal(t, len(containerMap), 0)
	})
}

func BuildMockClient(t *testing.T) (pb.ControllerClient, *grpc.ClientConn) {
	var err error
	connection, err := grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Errorf("Could not start client for testing purposes: %v.", err)
		return nil, nil
	}
	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)

	return testClient, connection
}

func ContainerExists(instanceID string) bool {
	// Check if the image is present
	switch *requestedRuntime {
	case "docker":
		_, err := runtime.Cli.ContainerInspect(context.Background(), instanceID)
		return err == nil
	case "mockRuntime":
		return fakeRuntime.ContainerExists(instanceID)
	default:
		return false
	}
}

func waitWithTimeout(swg *SafeWaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		swg.Wait()
		defer close(done)
	}()

	select {
	case <-done:
		return true

	case <-time.After(timeout):
		swg.Reset()
		return false
	}
}

func evaluateStatistics(statistics concurrencyStatistics) {
	log.Info().Msgf("Successfully started %v%% (%v of %v)", (float64(statistics.successfullyStarted)/float64(statistics.startAttempts))*100, statistics.successfullyStarted, statistics.startAttempts)
	log.Info().Msgf("Successfully called %v%% (%v of %v)", (float64(statistics.successfullyCalled)/float64(statistics.callAttempts))*100, statistics.successfullyCalled, statistics.callAttempts)
	log.Info().Msgf("Successfully stopped %v%% (%v of %v)", (float64(statistics.successfullyStopped)/float64(statistics.stopAttempts))*100, statistics.successfullyStopped, statistics.stopAttempts)
}
