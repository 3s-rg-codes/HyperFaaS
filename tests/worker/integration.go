package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DURATION    = 2 * time.Second
	ADDRESS     = "localhost:50051"
	RUNTIME     = "docker"
	ENVIRONMENT = "local"
	TIMEOUT     = 20
	LOG_DEFAULT = "info"
)

var (
	controllerServerAddress = flag.String("server-address", ADDRESS, "specify controller server adress")
	requestedRuntime        = flag.String("requested-runtime", RUNTIME, "specify container runtime")
	logLevel                = flag.String("log-level", LOG_DEFAULT, "specify the log level")
	autoRemove              = flag.Bool("auto-remove", true, "automatically remove containers")
	environment             = flag.String("environment", ENVIRONMENT, "choose environment for execution (local, compose")
	testCases               = flag.String("test-cases", "", "specify the test cases which should be run")
	dockerTolerance         = flag.Duration("dockerTolerance", DURATION, "Tolerance for container start and stop in seconds")
	listenerTimeout         = flag.Int("flag-timeout", TIMEOUT, "specify the timeout after listeners are removed")
	CPUPeriod               = flag.Int64("cpu-period", 100000, "CPU period")
	CPUQuota                = flag.Int64("cpu-quota", 50000, "CPU quota")
	MemoryLimit             = (*flag.Int64("memory-limit", 250000000, "Memory limit in MB")) * 1024 * 1024
)

var workloadImageTags = []string{"hyperfaas-hello:latest", "hyperfaas-echo:latest"}

type StatsTest struct {
	TestName            string
	Disconnects         bool
	Reconnects          bool
	NodeIDs             []string
	ControllerWorkloads []helpers.ControllerWorkload
	Timeout             time.Duration
}

var controllerWorkloads = []helpers.ControllerWorkload{
	{
		TestName:          "normal execution of hello image",
		ImageTag:          workloadImageTags[0],
		ExpectedError:     false,
		ExpectsResponse:   true,
		ExpectedErrorCode: codes.OK,
		CallPayload:       []byte(""),
	},
}

var statsTests = []StatsTest{
	{
		TestName:            "streaming stats to one connected node",
		Disconnects:         false,
		Reconnects:          false,
		NodeIDs:             []string{"1"},
		ControllerWorkloads: controllerWorkloads,
		Timeout:             12 * time.Second,
	},

	{
		TestName:            "streaming stats to three connected nodes",
		Disconnects:         false,
		Reconnects:          false,
		NodeIDs:             []string{"10", "20", "30"},
		ControllerWorkloads: controllerWorkloads,
		Timeout:             12 * time.Second,
	},

	{
		TestName:            "one node disconnects and reconnects while streaming stats",
		Disconnects:         true,
		Reconnects:          true,
		NodeIDs:             []string{"16"},
		ControllerWorkloads: controllerWorkloads,
		Timeout:             12 * time.Second,
	},
}

type Test struct {
	name string
	err  error
}

func main() {
	flag.Parse()

	logger := setupLogger(*logLevel)

	testCasesMax := 9
	testsMap := make(map[int]bool, 10)
	testAll := *environment == "compose" && *testCases == "all"

	if !testAll {
		for i := 1; i <= testCasesMax; i++ {
			if strings.Contains(*testCases, string(rune(i))) {
				testsMap[i] = true
			} else {
				testsMap[i] = false
			}
		}
	}

	var testController controller.Controller
	var runtime dockerRuntime.DockerRuntime
	var err error

	switch *environment {
	case "local":
		runtime = *dockerRuntime.NewDockerRuntime(*autoRemove, *environment, logger)
		testController = setupLocalEnv(runtime, logger, *listenerTimeout)
	case "compose":
		runtime, err = setUpComposeEnv(logger)
		if err != nil {
			logger.Error("Error occurred when building the client", "error", err)
			return
		}
	}

	time.Sleep(3 * time.Second) //wait for the other components to start

	client, connection, err := helpers.BuildMockClientHelper(*controllerServerAddress)
	if err != nil {
		logger.Error("Error occurred when building the client", "error", err)
		return
	}
	logger.Debug("Created client")

	defer func(connection *grpc.ClientConn) {
		err := connection.Close()
		if err != nil {
			logger.Error("Error occurred when closing the connection:", "error", err)
			return
		}
	}(connection)

	//////////////////////////////// Test Cases //////////////////////////////////////
	//TODO: we ideally want this to work without timeouts too

	testArray := make([]Test, 0)

	time.Sleep(5 * time.Second)

	if testsMap[1] || *environment == "local" || testAll {
		name := "normal execution"
		logger.Info("Starting test", "test", name)
		tErr := testNormalExecution(client, runtime, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[2] || *environment == "local" || testAll {
		name := "stop non existing container"
		logger.Info("Starting test", "test", name)
		tErr := testStopNonExistingContainer(client, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[3] || *environment == "local" || testAll {
		name := "call non existing container"
		logger.Info("Starting test", "test", name)
		tErr := testCallNonExistingContainer(client, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[4] || *environment == "local" || testAll {
		name := "test metrics"
		logger.Info("Starting test", "test", name)
		tErr := testMetrics(client, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[5] || *environment == "local" || testAll {
		name := "start non local image"
		logger.Info("Starting test", "test", name)
		tErr := testStartNonLocalImages(client, runtime, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[6] || *environment == "local" || testAll {
		name := "container config"
		logger.Info("Starting test", "test", name)
		tErr := testContainerConfig(runtime, client, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[7] || *environment == "local" || testAll {
		name := "one node as listener"
		logger.Info("Starting test", "test", name)
		tErr := testOneNodeListening(client, testController, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[8] || *environment == "local" || testAll {
		name := "multiple nodes listening"
		logger.Info("Starting test", "test", name)
		tErr := testMultipleNodesListening(client, testController, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	if testsMap[9] || *environment == "local" || testAll {
		name := "disconnect and reconnect listener"
		logger.Info("Starting test", "test", name)
		tErr := testDisconnectAndReconnect(client, testController, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
	}

	time.Sleep(5 * time.Second)

	////////////////////////////////////////////////////////////////////////////////////

	logger.Info("TEST RESULTS ")

	for _, elem := range testArray {
		if elem.err != nil {
			logger.Error("Test failed", "test", elem.name, "err", elem.err)
		} else {
			logger.Info("Test passed", "test", elem.name)
		}
	}

}

////////////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// MAIN INTEGRATION TESTS/////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////

var imageTags = []string{"hyperfaas-hello:latest", "hyperfaas-crash:latest", "hyperfaas-echo:latest", "hyperfaas-sleep:latest"}

var cases1 = []helpers.ControllerWorkload{
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

func testNormalExecution(client pb.ControllerClient, runtime dockerRuntime.DockerRuntime, logger slog.Logger) error {

	for _, tCase := range cases1 {
		testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: tCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

		grpcStatus, ok := status.FromError(err)
		if !ok {
			logger.Error("Start failed", "statusCode", grpcStatus.Code())
			return fmt.Errorf("starting container failed %v", grpcStatus.Code())
		}
		if testContainerID == nil {
			logger.Error("Error occurred starting container", "containerID", testContainerID)
			return fmt.Errorf("starting container failed, containerID is nil %v", grpcStatus.Code())
		}
		if !helpers.ContainerExists(testContainerID.Id, runtime) {
			logger.Error("Container did not start successfully", "containerID", testContainerID)
			return fmt.Errorf("starting container failed, container doesnt exist %v", grpcStatus.Code())
		}
		logger.Info("Container started successfully", "containerID", testContainerID)

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("GRPC response code for start is not as expected", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())
			return fmt.Errorf("grpc code is not as expected, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}
		logger.Info("GRPC response code for start is as expected", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())

		response, err := client.Call(context.Background(), &common.CallRequest{InstanceId: testContainerID, Data: tCase.CallPayload})

		grpcStatus, ok = status.FromError(err)

		if !ok {
			logger.Error("Call failed", "status code", grpcStatus.Code())
			return fmt.Errorf("calling container failed, code: %v", grpcStatus.Code())
		}

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("GRPC response code for call not is as expected", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())
			return fmt.Errorf("grpc code is not as expected, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}
		logger.Info("GRPC response code for call is as expected", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())

		if tCase.ExpectsResponse && string(tCase.ExpectedResponse) != string(response.Data) {
			logger.Error("Expected response and actual response don't match", "expected", string(tCase.ExpectedResponse), "actual", string(response.Data))
			return fmt.Errorf("expected response and actual response don't match, expected: %v, actual: %v", string(tCase.ExpectedResponse), string(response.Data))
		}
		logger.Info("Expected response and actual response match", "expected", string(tCase.ExpectedResponse), "actual", string(response.Data))

		//stop container
		responseContainerID, err := client.Stop(context.Background(), testContainerID)
		grpcStatus, ok = status.FromError(err)
		if !ok {
			logger.Error("Stop failed", "code", grpcStatus.Code())
			return fmt.Errorf("stopping container failed %v", grpcStatus.Code())
		}
		//TOLERANCE
		time.Sleep(*dockerTolerance)

		if responseContainerID.Id != testContainerID.Id || helpers.ContainerExists(responseContainerID.Id, runtime) { //dont know an easy way to check inside the container of the container still exists so just check in docker desktop??
			logger.Error("Container was not successfully deleted", "ID", testContainerID)
			return fmt.Errorf("container was not successfully deleted, code: %v", grpcStatus.Code())
		}

		logger.Info("Stop succeded", "containerID", responseContainerID.Id)
	}

	return nil
}

var cases2 = []helpers.ControllerWorkload{
	{
		TestName:          "stopping non existing container",
		InstanceID:        "nonExistingContainer",
		ExpectedError:     true,
		ExpectedErrorCode: codes.NotFound,
	},
}

func testStopNonExistingContainer(client pb.ControllerClient, logger slog.Logger) error {

	for _, tCase := range cases2 {

		_, err := client.Stop(context.Background(), &common.InstanceID{Id: tCase.InstanceID})

		grpcStatus, ok := status.FromError(err)

		if !ok {
			logger.Info("gRPC Error", "code", grpcStatus.Code())
		}

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("Error Codes do not align", "actual", grpcStatus.Code(), "expected", tCase.ExpectedErrorCode)
			return fmt.Errorf("grpc code is not as expected, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}

		logger.Info("Stopping unknown instance failed successfully", "code", grpcStatus.Code())

	}

	return nil

}

var cases3 = []helpers.ControllerWorkload{
	{
		TestName:          "calling non existing container",
		InstanceID:        "nonExistingContainer",
		ExpectedError:     true,
		ExpectedErrorCode: codes.NotFound,
	},
}

func testCallNonExistingContainer(client pb.ControllerClient, logger slog.Logger) error {

	for _, tCase := range cases3 {

		_, err := client.Call(context.Background(), &common.CallRequest{InstanceId: &common.InstanceID{Id: tCase.InstanceID}, Data: []byte("")})

		grpcStatus, ok := status.FromError(err)

		if !ok {
			logger.Error("gRPC Error", "code", grpcStatus.Code())
			return fmt.Errorf("calling container failed with unknown error %v", grpcStatus.Code())
		}

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("Expected and actual grpc codes dont match", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())
			return fmt.Errorf("expected and actual grpc codes dont match, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}

		logger.Info("Calling unknown instance failed successfully", "code", grpcStatus.Code())

	}
	return nil
}

func testMetrics(client pb.ControllerClient, logger slog.Logger) error {

	metrics, err := client.Metrics(context.Background(), &pb.MetricsRequest{NodeID: "a"})
	grpcStatus, ok := status.FromError(err)

	if !ok {
		logger.Error("Getting MetricsUpdate failed", "code", grpcStatus.Code())
		return fmt.Errorf("getting MetricsUpdate failed %v", grpcStatus.Code())
	}

	logger.Info("successfully got metrics")
	logger.Debug("Metrics are:", "ram", metrics.UsedRamPercent, "cpu", metrics.CpuPercentPercpu)

	return nil
}

var cases4 = []helpers.ControllerWorkload{
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

func testStartNonLocalImages(client pb.ControllerClient, runtime dockerRuntime.DockerRuntime, logger slog.Logger) error {

	for _, tCase := range cases4 {
		logger.Info("Scenario", "expectedError", tCase.ExpectedError)

		//Check if the image already exists locally

		opt := image.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "reference", Value: tCase.ImageTag}),
		}

		localImages, err := runtime.Cli.ImageList(context.Background(), opt)

		if err != nil {
			logger.Error("Could not list local images", "error", err)
			return fmt.Errorf("could not list local images, error: %v", err)
		}

		if len(localImages) > 0 {
			logger.Debug("Image already exists locally", "image", tCase.ImageTag)
			//erase image
			_, err := runtime.Cli.ImageRemove(context.Background(), localImages[0].ID, image.RemoveOptions{
				Force: true,
			})
			if err != nil {
				logger.Error("Could not remove local image", "error", err)
				return fmt.Errorf("could not delete local image, error: %v", err)
			}
		}

		_, err = client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: tCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

		grpcStatus, ok := status.FromError(err)

		if !ok {
			logger.Error("gRPC Error", "code", grpcStatus.Code())
			return fmt.Errorf("container was not started successfully, code: %v", grpcStatus.Code())
		}

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("Expected and actual grpc codes dont match", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())
			return fmt.Errorf("expected and actual grpc codes dont match, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}

		if tCase.ExpectedError {
			logger.Info("Starting unknown image failed successfully", "code", grpcStatus.Code())
		} else {
			logger.Info("Remote image was pulled and started successfully", "image-tag", tCase.ImageTag)
		}

	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// CONTAINER CONFIG TESTS ////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////

func testContainerConfig(runtime dockerRuntime.DockerRuntime, client pb.ControllerClient, logger slog.Logger) error {

	spec := helpers.ResourceSpec{
		CPUPeriod:   *CPUPeriod,
		CPUQuota:    *CPUQuota,
		MemoryLimit: MemoryLimit,
	}

	cID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: "hyperfaas-hello:latest"}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: spec.CPUPeriod, Quota: spec.CPUQuota}, Memory: spec.MemoryLimit}})
	if err != nil {
		logger.Error("Error occurred starting container")
		return err
	}

	st, _ := runtime.Cli.ContainerStats(context.Background(), cID.Id, false)

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(st.Body)
	if err != nil {
		logger.Error("Error reading response bytes", "error", err)
		return err
	}

	var cStats ContainerStats
	err = json.Unmarshal(buf.Bytes(), &cStats)
	if err != nil {
		logger.Error("error unmarshaling JSON", "error", err)
		return err
	}
	// Print the relevant values
	logger.Debug("Configured Memory Limit:", "bytes", cStats.MemoryStats.Limit)
	logger.Debug("Configured CPU Periods", "periods", cStats.CPUStats.ThrottlingData.Periods)
	logger.Debug("Configured CPU Quota (Throttled Time)", "quota", cStats.CPUStats.ThrottlingData.ThrottledTime)
	//t.Logf("Container inspect: %v , %v , %v", a, a.Config, a.NetworkSettings)

	_, err = client.Stop(context.Background(), cID)
	if err != nil {
		logger.Error("Error stopping container", "error", err)
		return err
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////////////
///////////////////////////////////// STATS TESTS //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////

func testOneNodeListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) error {

	statsChan := make(chan *[]*stats.StatusUpdate)
	errorChan := make(chan error, 10)
	wgWorkload := sync.WaitGroup{}

	wgWorkload.Add(1)
	go func(wg *sync.WaitGroup, errCh chan error) {
		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		errCh <- err
		wg.Done()
		statsChan <- expectedStats

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	resultChannel := make(chan []*stats.StatusUpdate)
	stopSignals := make(map[string]chan bool)

	nodeID := statsTests[0].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, errCh chan error, wg *sync.WaitGroup) {
		stopSignal := stopSignals[nodeID]

		logger.Debug("Connecting listener node")
		//TODO: currently the nodes are running in the client node when using docker compose, which kinda misses the point
		actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, wg, stopSignal, statsTests[1].Timeout)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}
		errCh <- err
		ch <- actualNodeStats
	}(resultChannel, errorChan, &wgNodes)

	wgWorkload.Wait()

	for len(errorChan) > 0 {
		e := <-errorChan
		if e != nil {
			return e
		}
	}
	expected := <-statsChan
	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := helpers.Evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
		return err
	}

	if result {
		logger.Info("=====TEST `oneNodeListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `oneNodeListening` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	if *environment == "local" {
		testController.StatsManager.RemoveListener(nodeID)
	}

	return nil

}

func testMultipleNodesListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) error {

	wgWorkload := sync.WaitGroup{}
	errorChan := make(chan error, 10)
	statsChan := make(chan *[]*stats.StatusUpdate)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup, errCh chan error) {

		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		errCh <- err
		logger.Debug("Workload is done")
		wg1.Done()
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	stopSignals := make(map[string]chan bool)
	resultChannels := make(map[string]chan []*stats.StatusUpdate)

	for _, nodeID := range statsTests[1].NodeIDs {

		wgNodes.Add(1)
		resultChannels[nodeID] = make(chan []*stats.StatusUpdate, 1)
		ch := resultChannels[nodeID]

		go func(ch chan []*stats.StatusUpdate, nodeId string, errCh chan error) {
			stopSignal := stopSignals[nodeId]

			actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeId, logger, &wgNodes, stopSignal, statsTests[1].Timeout)
			if err != nil {
				logger.Error("Error when connecting node", "error", err)
			}
			errCh <- err
			ch <- actualNodeStats
			logger.Info("Sent stats to channel and now leaving goroutine", "nodeId", nodeID)
		}(ch, nodeID, errorChan)
	}

	wgWorkload.Wait()
	for len(errorChan) > 0 {
		var e error
		select {
		case e = <-errorChan:
		case <-time.After(10 * time.Second):
			e = fmt.Errorf("timeout occured when waitng for error")
		}
		if e != nil {
			return e
		}
	}
	expected := <-statsChan
	wgNodes.Wait()
	result := true

	for _, nodeID := range statsTests[1].NodeIDs {
		receivedStats := <-resultChannels[nodeID]
		res, err := helpers.Evaluate(receivedStats, *expected)
		if err != nil {
			logger.Error("Error evaluating test results for node", "err", err, "node", nodeID)
			return err
		}

		if !res {
			result = false
		}

		if *environment == "local" {
			testController.StatsManager.RemoveListener(nodeID)
		}
	}

	if result {
		logger.Info("=====TEST `multipleNodesListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `multipleNodesListening` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	return nil

}

func testDisconnectAndReconnect(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) error {

	wgWorkload := sync.WaitGroup{}
	statsChan := make(chan *[]*stats.StatusUpdate)
	errorChan := make(chan error, 10)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup, errCh chan error) {

		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		logger.Debug("Workload is done")
		wg1.Done()
		errCh <- err
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	stopSignals := make(map[string]chan bool)

	go func() {
		for _, nodeID := range statsTests[2].NodeIDs {
			stopSignals[nodeID] = make(chan bool)
			logger.Info("Node will disconnect", "node", nodeID)
			time.Sleep(3 * time.Second)
			stopSignals[nodeID] <- true
		}
	}()

	resultChannel := make(chan []*stats.StatusUpdate, 10)

	nodeID := statsTests[2].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string, errCh chan error) {
		stopSignal := stopSignals[nodeID]

		actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, &wgNodes, stopSignal, statsTests[2].Timeout)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}

		errCh <- err
		ch <- actualNodeStats
	}(resultChannel, nodeID, errorChan)

	wgWorkload.Wait()
	expected := <-statsChan
	for len(errorChan) > 0 {
		e := <-errorChan
		if e != nil {
			return e
		}
	}
	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string) {
		stopSignal := stopSignals[nodeID]
		actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, &wgNodes, stopSignal, statsTests[2].Timeout)
		if err != nil {
			logger.Error("Error when reconnecting node", "error", err)
		}

		ch <- actualNodeStats
	}(resultChannel, nodeID)

	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := helpers.Evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
		return err
	}

	if result {
		logger.Info("=====TEST `DisconnectAndReconnect` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `DisconnectAndReconnect` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	if *environment == "local" {
		testController.StatsManager.RemoveListener(nodeID)
	}
	return nil
}

func setupLocalEnv(runtime dockerRuntime.DockerRuntime, logger *slog.Logger, timeout int) controller.Controller {

	testController := *controller.NewController(&runtime, logger, *controllerServerAddress, time.Duration(timeout))

	logger.Debug("Created controller")
	//CallerServer
	go func() {
		testController.StartServer()
	}()
	logger.Debug("Started controller")

	return testController

}

func setUpComposeEnv(logger *slog.Logger) (dockerRuntime.DockerRuntime, error) {

	dockerCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not create Docker client", "error", err)
		return dockerRuntime.DockerRuntime{}, err
	}
	runtime := *dockerRuntime.NewDockerRuntime(*autoRemove, "", logger)
	runtime.Cli = dockerCli
	return runtime, nil

}

type ContainerStats struct {
	MemoryStats struct {
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	CPUStats struct {
		ThrottlingData struct {
			Periods       uint64 `json:"periods"`
			ThrottledTime uint64 `json:"throttled_time"`
		} `json:"throttling_data"`
	} `json:"cpu_stats"`
}

func setupLogger(logLevel string) *slog.Logger {
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout,
		&slog.HandlerOptions{
			Level:     level,
			AddSource: true}))

	return logger
}
