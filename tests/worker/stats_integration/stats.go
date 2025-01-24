package main

import (
	"flag"
	"fmt"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"log/slog"
	"os"
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

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
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

		actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, &wgNodes, stopSignal, statsTests[1].Timeout)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}
		ch <- actualNodeStats
	}(resultChannel)

	wgWorkload.Wait()
	expected := <-statsChan
	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := helpers.Evaluate(receivedStats, *expected)
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

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
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

			actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, &wgNodes, stopSignal, statsTests[1].Timeout)
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
		res, err := helpers.Evaluate(receivedStats, *expected)
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

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, controllerWorkloads[0])
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

		actualNodeStats, err := helpers.ConnectNodeHelper(*controllerServerAddress, nodeID, logger, &wgNodes, stopSignal, statsTests[2].Timeout)
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
	}

	if result {
		logger.Info("=====TEST `multipleNodesListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `multipleNodesListening` COMPLETED: FAILED")
	}

	testController.StatsManager.RemoveListener(nodeID)

}
