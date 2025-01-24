package main

import (
	"context"
	"flag"
	"fmt"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"time"
)

const (
	ADDRESS     = "localhost:50051"
	RUNTIME     = "docker"
	ENVIRONMENT = "local"
)

var (
	controllerServerAddress = flag.String("ServerAdress", ADDRESS, "specify controller server adress")
	requestedRuntime        = flag.String("Requested Runtime", RUNTIME, "specify container runtime")
	autoRemove              = flag.Bool("autoRemove", true, "automatically remove containers")
	environment             = flag.String("environment", ENVIRONMENT, "choose environment for execution (local, compose")
	CPUPeriod               = flag.Int64("cpuPeriod", 100000, "CPU period")
	CPUQuota                = flag.Int64("cpuQuota", 50000, "CPU quota")
	MemoryLimit             = (*flag.Int64("memoryLimit", 250000000, "Memory limit in MB")) * 1024 * 1024
)

var (
	runtime dockerRuntime.DockerRuntime
)

type ControllerWorkload struct {
	TestName          string
	ImageTag          string
	ExpectedError     bool
	ReturnError       bool
	ExpectsResponse   bool
	ExpectedResponse  []byte
	ErrorCode         codes.Code
	ExpectedErrorCode codes.Code
	CallPayload       []byte
	InstanceID        string
}

type StatsTest struct {
	TestName            string
	Disconnects         bool
	Reconnects          bool
	NodeIDs             []string
	ControllerWorkloads []ControllerWorkload
	Timeout             time.Duration
}

var controllerWorkloads = []ControllerWorkload{
	{
		TestName:          "normal execution of hello image",
		ImageTag:          workloadImageTags[0],
		ExpectedError:     false,
		ExpectsResponse:   true,
		ExpectedErrorCode: codes.OK,
		CallPayload:       []byte(""),
	},
}

var workloadImageTags = []string{"hyperfaas-hello:latest", "hyperfaas-echo:latest"}

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

func main() {

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	logger.Info("Test Configuration: ")
	flag.Parse()
	flag.VisitAll(func(f *flag.Flag) {
		isDefault := "false"
		if f.DefValue == f.Value.String() {
			isDefault = "true"
		}
		if f.Name[:5] != "test." || f.Name == "update" {
			logger.Info("FLAG:", "name", f.Name, "value", f.Value.String(), "isDefault", isDefault)
		}
	})
	fmt.Println()

	switch *requestedRuntime {
	case "docker":
		runtime = *dockerRuntime.NewDockerRuntime(*autoRemove, *environment, logger) //did not work otherwise, using container runtime interface
	}
	logger.Debug("Created runtime")

	testController := controller.NewController(&runtime, logger, *controllerServerAddress)

	logger.Debug("Created controller")
	//CallerServer
	go func() {
		testController.StartServer()
	}()
	logger.Debug("Started controller")

	client, connection, err := helpers.BuildMockClientHelper(*controllerServerAddress)
	if err != nil {
		logger.Error("Error occurred when building the client", "error", err)
	}
	logger.Debug("Created client")

	defer func(connection *grpc.ClientConn) {
		err := connection.Close()
		if err != nil {
			logger.Error("Error occurred when closing the connection:", "error", err)
		}
	}(connection)

	time.Sleep(4 * time.Second)

	testOneNodeListening(client, *testController, *logger)

	testMultipleNodesListening(client, *testController, *logger)

	testDisconnectAndReconnect(client, *testController, *logger)

}

func testOneNodeListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) {
	logger.Info("Starting Test `oneNodeListening`")

	statsChan := make(chan *[]*stats.StatusUpdate)
	wgWorkload := sync.WaitGroup{}

	wgWorkload.Add(1)
	go func(wg *sync.WaitGroup) {
		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := doWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		logger.Debug("Workload is done")
		wg.Done()
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload)

	wgNodes := sync.WaitGroup{}

	resultChannel := make(chan []*stats.StatusUpdate)
	stopSignals := make(map[string]chan bool)

	nodeID := statsTests[0].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate) {
		stopSignal := stopSignals[nodeID]

		actualNodeStats, err := connectNodeHelper(nodeID, logger, &wgNodes, stopSignal, statsTests[1].Timeout)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}
		ch <- actualNodeStats
	}(resultChannel)

	wgWorkload.Wait()
	expected := <-statsChan
	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
	}

	if result {
		logger.Info("=====TEST `oneNodeListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `oneNodeListening` COMPLETED: FAILED")
	}

	testController.StatsManager.RemoveListener(nodeID)

}

func testMultipleNodesListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) {
	logger.Info("Starting Test `multipleNodesListening`")

	wgWorkload := sync.WaitGroup{}
	statsChan := make(chan *[]*stats.StatusUpdate)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup) {

		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := doWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		logger.Debug("Workload is done")
		wg1.Done()
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload)

	wgNodes := sync.WaitGroup{}

	stopSignals := make(map[string]chan bool)
	resultChannels := make(map[string]chan []*stats.StatusUpdate)

	logger.Debug("Entering for loop for distribution of events", "iterations", len(statsTests[1].NodeIDs))
	for _, nodeID := range statsTests[1].NodeIDs {

		wgNodes.Add(1)
		resultChannels[nodeID] = make(chan []*stats.StatusUpdate, 1)
		ch := resultChannels[nodeID]

		go func(ch chan []*stats.StatusUpdate) {
			stopSignal := stopSignals[nodeID]

			actualNodeStats, err := connectNodeHelper(nodeID, logger, &wgNodes, stopSignal, statsTests[1].Timeout)
			if err != nil {
				logger.Error("Error when connecting node", "error", err)
			}

			ch <- actualNodeStats
		}(ch)
	}

	wgWorkload.Wait()
	expected := <-statsChan
	wgNodes.Wait()

	result := true

	for _, nodeID := range statsTests[1].NodeIDs {
		receivedStats := <-resultChannels[nodeID]
		res, err := evaluate(receivedStats, *expected)
		if err != nil {
			logger.Error("Error evaluating test results", "err", err)
		}

		if !res {
			result = false
		}

		testController.StatsManager.RemoveListener(nodeID)
	}

	if result {
		logger.Info("=====TEST `multipleNodesListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `multipleNodesListening` COMPLETED: FAILED")
	}

}

func testDisconnectAndReconnect(client pb.ControllerClient, testController controller.Controller, logger slog.Logger) {
	logger.Info("Starting Test `multipleNodesListening`")

	wgWorkload := sync.WaitGroup{}
	statsChan := make(chan *[]*stats.StatusUpdate)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup) {

		spec := helpers.ResourceSpec{
			CPUPeriod:   *CPUPeriod,
			CPUQuota:    *CPUQuota,
			MemoryLimit: MemoryLimit,
		}

		expectedStats, err := doWorkloadHelper(client, logger, spec, controllerWorkloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		logger.Debug("Workload is done")
		wg1.Done()
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload)

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

	logger.Debug("Entering for loop for distribution of events", "iterations", len(statsTests[2].NodeIDs))
	nodeID := statsTests[1].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string) {
		stopSignal := stopSignals[nodeID]

		actualNodeStats, err := connectNodeHelper(nodeID, logger, &wgNodes, stopSignal, statsTests[2].Timeout)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}

		ch <- actualNodeStats
	}(resultChannel, nodeID)

	wgWorkload.Wait()
	expected := <-statsChan

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string) {
		stopSignal := stopSignals[nodeID]
		actualNodeStats, err := connectNodeHelper(nodeID, logger, &wgNodes, stopSignal, statsTests[2].Timeout)
		if err != nil {
			logger.Error("Error when reconnecting node", "error", err)
		}

		ch <- actualNodeStats
	}(resultChannel, nodeID)

	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
	}

	if result {
		logger.Info("=====TEST `multipleNodesListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `multipleNodesListening` COMPLETED: FAILED")
	}

	testController.StatsManager.RemoveListener(nodeID)

}

func doWorkloadHelper(client pb.ControllerClient, logger slog.Logger, spec helpers.ResourceSpec, testCase ControllerWorkload) (*[]*stats.StatusUpdate, error) {

	cID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: spec.CPUPeriod, Quota: spec.CPUQuota}, Memory: spec.MemoryLimit}})
	if err != nil {
		return nil, err
	}

	logger.Debug("Started container", "container", cID)

	var statusUpdates []*stats.StatusUpdate

	if testCase.ExpectedError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: "container", Event: "start", Status: "error"})
	} else {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: "container", Event: "start", Status: "success"})
	}

	response, err := client.Call(context.Background(), &common.CallRequest{InstanceId: cID, Data: testCase.CallPayload})
	if err != nil {
		return nil, err
	}
	logger.Debug("Called container", "response", response.Data)

	if testCase.ExpectedError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: "container", Event: "call", Status: "error"})
	} else {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: "container", Event: "call", Status: "success"})
	}
	//If there was a response, there is a container response event
	if testCase.ExpectsResponse && response != nil {
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: "container", Event: "response", Status: "success"})
	}

	responseContainerID, err := client.Stop(context.Background(), cID)
	if err != nil {
		return nil, err
	}
	logger.Debug("Stopped container", "container", responseContainerID)

	if testCase.ExpectedError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "error"})
	} else if responseContainerID != nil && responseContainerID.Id == cID.Id {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "success"})
	}

	//UNCOMMENT THIS TO INTENTIONALLY FAIL THE TEST
	//statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: responseContainerID.Id, Type: "break", Event: "break", Status: "break"})
	return &statusUpdates, nil
}

func connectNodeHelper(nodeID string, logger slog.Logger, wg *sync.WaitGroup, stopSignal chan bool, timeout time.Duration) ([]*stats.StatusUpdate, error) {

	client, conn, err := helpers.BuildMockClientHelper(*controllerServerAddress)
	if err != nil {
		logger.Error("Error creating client", "error", err.Error())
	}
	logger.Debug("Created listener node as client", "nodeID", nodeID)

	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			logger.Error("Error occurred closing connection", "error", err.Error())
		}
	}(conn)

	var receivedStats []*stats.StatusUpdate
	defer wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s, err := client.Status(ctx, &pb.StatusRequest{NodeID: nodeID})
	if err != nil {
		return nil, err
	}

	for {
		select {
		case <-stopSignal:
			return receivedStats, fmt.Errorf("stop signal received")

		case <-ctx.Done():
			return receivedStats, fmt.Errorf("context hit timeout")

		default:
			stat, err := s.Recv()
			if status.Code(err) == codes.DeadlineExceeded { //This will happen when the call finishes and we try to reach the node
				return receivedStats, nil
			}
			if err != nil {
				return receivedStats, fmt.Errorf("error: %v", err)
			}

			logger.Debug("Received stat", "stat", stat)
			// Copy the stats to a new struct to avoid copying mutex
			statCopy := &stats.StatusUpdate{
				InstanceID: stat.InstanceId,
				Type:       stat.Type,
				Event:      stat.Event,
				Status:     stat.Status,
			}

			receivedStats = append(receivedStats, statCopy)
		}

		if ctx.Err() != nil {
			break
		}
	}
	return receivedStats, nil
}

func evaluate(actual []*stats.StatusUpdate, expected []*stats.StatusUpdate) (bool, error) {

	if len(expected) != len(actual) {
		return false, fmt.Errorf("unequal length of expected (%v) and actual (%v) list", len(expected), len(actual))
	}

	//  count occurrences of each StatusUpdate in the expected array
	expectedCount := make(map[stats.StatusUpdate]int)
	for _, e := range expected {
		expectedCount[*e]++
	}

	//  count occurrences of each StatusUpdate in the actual array
	actualCount := make(map[stats.StatusUpdate]int)
	for _, a := range actual {
		actualCount[*a]++
	}

	// compare
	result := reflect.DeepEqual(expectedCount, actualCount)

	return result, nil
}
