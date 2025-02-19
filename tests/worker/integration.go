package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/mock"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Flags struct {
	ServerAddress    *string
	RequestedRuntime *string
	LogLevel         *string
	AutoRemove       *bool
	Environment      *string
	TestCases        *string
	DockerTolerance  *int
	ListenerTimeout  *int
	CPUPeriod        *int
	CPUQuota         *int
	MemoryLimit      *int
}

type FullConfig struct {
	Config    TestConfig                   `json:"test_config_defaults"`
	Stats     []StatsTest                  `json:"stats_test_cases"`
	Workloads []helpers.ControllerWorkload `json:"controller_workloads"`
}

type TestConfig struct {
	ServerAddress    string `json:"server_address"`
	RequestedRuntime string `json:"requested_runtime"`
	LogLevel         string `json:"log_level"`
	AutoRemove       bool   `json:"auto_remove"`
	Environment      string `json:"environment"`
	TestCases        string `json:"test_cases"`
	DockerTolerance  int    `json:"docker_tolerance"`
	ListenerTimeout  int    `json:"listener_timeout"`
	CPUPeriod        int    `json:"cpu_period"`
	CPUQuota         int    `json:"cpu_quota"`
	MemoryLimit      int    `json:"memory_limit"`
}

type StatsTest struct {
	Name        string   `json:"name"`
	Disconnects bool     `json:"disconnects"`
	Reconnects  bool     `json:"reconnects"`
	NodeIDs     []string `json:"nodeIDs"`
	Timeout     int      `json:"timeout"`
}

type Test struct {
	name string
	err  error
}

func main() {
	flags := Flags{
		ServerAddress:    flag.String("server_address", "", "Server address"),
		RequestedRuntime: flag.String("requested_runtime", "", "Requested runtime"),
		LogLevel:         flag.String("log_level", "", "Log level"),
		AutoRemove:       flag.Bool("auto_remove", true, "Auto remove"),
		Environment:      flag.String("environment", "", "Environment"),
		TestCases:        flag.String("test_cases", "", "test cases to run"),
		DockerTolerance:  flag.Int("docker_tolerance", 0, "Docker tolerance"),
		ListenerTimeout:  flag.Int("listener_timeout", 0, "Listener timeout"),
		CPUPeriod:        flag.Int("cpu_period", 0, "CPU period"),
		CPUQuota:         flag.Int("cpu_quota", 0, "CPU quota"),
		MemoryLimit:      flag.Int("memory_limit", 0, "Memory limit"),
	}

	flag.Parse()

	config := parseJSON()

	config.Config.UpdateFromFlags(flags)

	logger := setupLogger(config.Config.LogLevel)

	config.Config.printConfig(*logger)

	testCasesMax := 9
	testsMap := make(map[int]bool, 10)
	testAll := config.Config.Environment == "compose" && config.Config.TestCases == "all"

	if !testAll {
		for i := 1; i <= testCasesMax; i++ {
			testCaseStr := strconv.Itoa(i) // Convert number to string
			if strings.Contains(config.Config.TestCases, testCaseStr) {
				testsMap[i] = true
			} else {
				testsMap[i] = false
			}
		}
	}

	var testController controller.Controller
	var runtime containerRuntime.ContainerRuntime
	var err error

	callerServer := caller.NewCallerServer(logger)

	switch config.Config.RequestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(config.Config.AutoRemove, config.Config.Environment, logger)
	case "fake":
		runtime = mock.NewMockRuntime(callerServer, logger)
		testsMap[6] = false
		logger.Info("Mock runtime and container config test not compatible: Removing...")
	}

	switch config.Config.Environment {
	case "local":
		testController = setupLocalEnv(runtime, callerServer, logger, config.Config)
	case "compose":
		runtime, err = setUpComposeEnv(logger, config.Config)
		if err != nil {
			logger.Error("Error occurred when building the controllerClient", "error", err)
			return
		}
	}

	time.Sleep(3 * time.Second) //wait for the other components to start

	controllerClient, connection, err := helpers.BuildMockClientHelper(config.Config.ServerAddress)
	if err != nil {
		logger.Error("Error occurred when building the controllerClient", "error", err)
		return
	}
	logger.Debug("Created controllerClient")

	defer func(connection *grpc.ClientConn) {
		err := connection.Close()
		if err != nil {
			logger.Error("Error occurred when closing the connection:", "error", err)
			return
		}
	}(connection)

	spec := helpers.ResourceSpec{
		CPUPeriod:   int64(config.Config.CPUPeriod),
		CPUQuota:    int64(config.Config.CPUQuota),
		MemoryLimit: int64(config.Config.MemoryLimit * 1024 * 1024),
	}

	//////////////////////////////// Test Cases //////////////////////////////////////
	//TODO: we ideally want this to work without timeouts too

	testArray := make([]Test, 0)

	time.Sleep(5 * time.Second)

	if testsMap[1] || testAll {
		name := "normal execution"
		logger.Info("Starting test", "test", name)
		tErr := testNormalExecution(controllerClient, runtime, *logger, spec, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[2] || testAll {
		name := "stop non existing container"
		logger.Info("Starting test", "test", name)
		tErr := testStopNonExistingContainer(controllerClient, *logger, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[3] || testAll {
		name := "call non existing container"
		logger.Info("Starting test", "test", name)
		tErr := testCallNonExistingContainer(controllerClient, *logger, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[4] || testAll {
		name := "test metrics"
		logger.Info("Starting test", "test", name)
		tErr := testMetrics(controllerClient, *logger)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[5] || testAll {
		name := "start non local image"
		logger.Info("Starting test", "test", name)
		tErr := testStartNonLocalImages(controllerClient, runtime, *logger, spec, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[6] || testAll {
		name := "container config"
		logger.Info("Starting test", "test", name)
		tErr := TestContainerConfig(runtime, controllerClient, *logger, config.Config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[7] || testAll {
		name := "one node as listener"
		logger.Info("Starting test", "test", name)
		tErr := TestOneNodeListening(controllerClient, testController, *logger, spec, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[8] || testAll {
		name := "multiple nodes listening"
		logger.Info("Starting test", "test", name)
		tErr := TestMultipleNodesListening(controllerClient, testController, *logger, spec, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

	if testsMap[9] || testAll {
		name := "disconnect and reconnect listener"
		logger.Info("Starting test", "test", name)
		tErr := TestDisconnectAndReconnect(controllerClient, testController, *logger, spec, config)
		test := Test{name: name, err: tErr}
		testArray = append(testArray, test)
		time.Sleep(5 * time.Second)
	}

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

func testNormalExecution(client pb.ControllerClient, runtime containerRuntime.ContainerRuntime, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {

	for i := 0; i < 2; i++ {
		tCase := config.Workloads[i]
		testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: tCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: spec.CPUPeriod, Quota: spec.CPUQuota}, Memory: spec.MemoryLimit}})

		grpcStatus, ok := status.FromError(err)
		if !ok {
			logger.Error("Start failed", "statusCode", grpcStatus.Code())
			return fmt.Errorf("starting container failed %v", grpcStatus.Code())
		}
		if testContainerID == nil {
			logger.Error("Error occurred starting container", "containerID", testContainerID)
			return fmt.Errorf("starting container failed, containerID is nil %v", grpcStatus.Code())
		}
		if !runtime.ContainerExists(context.Background(), testContainerID.Id) {
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
		logger.Debug("Sleeping", "time", config.Config.DockerTolerance)
		tolerance := time.Duration(config.Config.DockerTolerance)
		time.Sleep(tolerance * time.Second)
		logger.Debug("Sleeping done")

		if responseContainerID.Id != testContainerID.Id || runtime.ContainerExists(context.Background(), responseContainerID.Id) {
			logger.Error("Container was not successfully deleted", "ID", testContainerID)
			return fmt.Errorf("container was not successfully deleted, code: %v", grpcStatus.Code())
		}

		logger.Info("Stop succeded", "containerID", responseContainerID.Id)
	}

	return nil
}

func testStopNonExistingContainer(client pb.ControllerClient, logger slog.Logger, config FullConfig) error {

	tCase := config.Workloads[2]

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

	return nil

}

func testCallNonExistingContainer(client pb.ControllerClient, logger slog.Logger, config FullConfig) error {

	tCase := config.Workloads[4]

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

func testStartNonLocalImages(client pb.ControllerClient, runtime containerRuntime.ContainerRuntime, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {

	for i := 4; i < 6; i++ {
		tCase := config.Workloads[i]
		logger.Info("Scenario", "expectedError", tCase.ExpectsError)

		//Check if the image already exists locally
		err := runtime.RemoveImage(context.Background(), tCase.TestName)
		if err != nil {
			return err
		}

		_, err = client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: tCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: spec.CPUPeriod, Quota: spec.CPUQuota}, Memory: spec.MemoryLimit}})

		grpcStatus, ok := status.FromError(err)

		if !ok {
			logger.Error("gRPC Error", "code", grpcStatus.Code())
			return fmt.Errorf("container was not started successfully, code: %v", grpcStatus.Code())
		}

		if grpcStatus.Code() != tCase.ExpectedErrorCode {
			logger.Error("Expected and actual grpc codes dont match", "expected", tCase.ExpectedErrorCode, "actual", grpcStatus.Code())
			return fmt.Errorf("expected and actual grpc codes dont match, expected: %v, actual: %v", tCase.ExpectedErrorCode, grpcStatus.Code())
		}

		if tCase.ExpectsError {
			logger.Info("Starting unknown image failed successfully", "code", grpcStatus.Code())
		} else {
			logger.Info("Remote image was pulled and started successfully", "image-tag", tCase.ImageTag)
		}

	}

	return nil
}

func setupLocalEnv(runtime containerRuntime.ContainerRuntime, callerServer *caller.CallerServer, logger *slog.Logger, config TestConfig) controller.Controller {

	testController := *controller.NewController(runtime, callerServer, logger, config.ServerAddress, time.Duration(config.ListenerTimeout)*time.Second)

	logger.Debug("Created controller")
	//CallerServer
	go func() {
		testController.StartServer()
	}()
	logger.Debug("Started controller")

	return testController

}

func setUpComposeEnv(logger *slog.Logger, config TestConfig) (containerRuntime.ContainerRuntime, error) {

	dockerCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not create Docker client", "error", err)
		return nil, err
	}
	runtime := dockerRuntime.NewDockerRuntime(config.AutoRemove, "", logger)
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

func parseJSON() FullConfig {
	data, err := os.ReadFile("test_config.json")
	if err != nil {
		panic(err)
	}

	var config FullConfig
	if err := json.Unmarshal(data, &config); err != nil {
		panic(err)
	}

	return config
}

func (c *TestConfig) UpdateFromFlags(f Flags) {

	if *f.ServerAddress != "" {
		c.ServerAddress = *f.ServerAddress
	}

	if *f.RequestedRuntime != "" {
		c.RequestedRuntime = *f.RequestedRuntime
	}

	if *f.LogLevel != "" {
		c.LogLevel = *f.LogLevel
	}

	if *f.AutoRemove != true {
		c.AutoRemove = *f.AutoRemove
	}

	if *f.Environment != "" {
		c.Environment = *f.Environment
	}

	if *f.TestCases != "" {
		c.TestCases = *f.TestCases
	}

	if *f.DockerTolerance != 0 {
		c.DockerTolerance = *f.DockerTolerance
	}

	if *f.ListenerTimeout != 0 {
		c.ListenerTimeout = *f.ListenerTimeout
	}

	if *f.CPUPeriod != 0 {
		c.CPUPeriod = *f.CPUPeriod
	}

	if *f.CPUQuota != 0 {
		c.CPUQuota = *f.CPUQuota
	}

	if *f.MemoryLimit != 0 {
		c.MemoryLimit = *f.MemoryLimit
	}
}

func (c *TestConfig) printConfig(logger slog.Logger) {
	logger.Info("Test Configuration",
		"ServerAddress", c.ServerAddress,
		"RequestedRuntime", c.RequestedRuntime,
		"LogLevel", c.LogLevel,
		"AutoRemove", c.AutoRemove,
		"Environment", c.Environment,
		"TestCases", c.TestCases,
		"DockerTolerance", c.DockerTolerance,
		"ListenerTimeout", c.ListenerTimeout,
		"CPUPeriod", c.CPUPeriod,
		"CPUQuota", c.CPUQuota,
		"MemoryLimit", c.MemoryLimit,
	)
}
