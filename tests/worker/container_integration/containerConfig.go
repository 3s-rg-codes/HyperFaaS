package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"google.golang.org/grpc"
	"log/slog"
	"os"
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

// Container CPU and Memory configuration is OS dependent. Currently, the CPU and Memory configurations are implemented for our Docker containers,
// but it is not clear how to verify the configurations. This test starts a container and prints the configurations obtained from ContainerStats.
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	err = testContainerConfig(runtime, client, *logger)
	if err == nil {
		logger.Info("=====TEST `testContainerConfig` COMPLETED: SUCCESS")
	}
}

func testContainerConfig(runtime dockerRuntime.DockerRuntime, client pb.ControllerClient, logger slog.Logger) error {
	logger.Info("Starting `testContainerConfig` test")

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

	respString := buf.String()
	logger.Info("Container stats", "stats", respString)

	var stats ContainerStats
	err = json.Unmarshal(buf.Bytes(), &stats)
	if err != nil {
		logger.Error("error unmarshaling JSON", "error", err)
	}
	// Print the relevant values
	logger.Info("Configured Memory Limit:", "bytes", stats.MemoryStats.Limit)
	logger.Info("Configured CPU Periods", "periods", stats.CPUStats.ThrottlingData.Periods)
	logger.Info("Configured CPU Quota (Throttled Time)", "quota", stats.CPUStats.ThrottlingData.ThrottledTime)
	//t.Logf("Container inspect: %v , %v , %v", a, a.Config, a.NetworkSettings)

	_, err = client.Stop(context.Background(), cID)
	if err != nil {
		logger.Error("Error stopping container", "error", err)
		return err
	}

	return nil
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
