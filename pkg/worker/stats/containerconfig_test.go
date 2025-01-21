package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"github.com/3s-rg-codes/HyperFaaS/helpers"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"log/slog"
	"testing"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
)

var (
	requestedRuntime = flag.String("specifyRuntime", helpers.RUNTIME, "for now only docker, is also default")
	autoRemove       = flag.Bool("autoRemove", true, "specify if containers should be removed after stopping")
)

// Container CPU and Memory configuration is OS dependent. Currently the CPU and Memory configurations are implemented for our Docker containers,
// but it is not clear how to verify the configurations. This test starts a container and prints the configurations obtained from ContainerStats.
func TestContainerConfig(t *testing.T) {

	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: helpers.ImageTags[0]}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	//_, _ := runtime.Cli.ContainerInspect(context.Background(), testContainerID.Id)
	var runtime *dockerRuntime.DockerRuntime

	switch *requestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(*autoRemove, slog.Default().With("runtime", "docker")) //did not work otherwise, using container runtime interface
	}

	if runtime == nil {
		t.Fatalf("Error creating runtime")
	}

	st, _ := runtime.Cli.ContainerStats(context.Background(), testContainerID.Id, false)

	buf := new(bytes.Buffer)
	buf.ReadFrom(st.Body)
	respBytes := buf.String()
	respString := string(respBytes)
	t.Logf("Container stats: %v", respString)

	var stats ContainerStats
	err = json.Unmarshal(buf.Bytes(), &stats)
	if err != nil {
		t.Fatalf("error unmarshaling JSON: %v", err)
	}
	// Print the relevant values
	t.Logf("Configured Memory Limit: %d bytes\n", stats.MemoryStats.Limit)
	t.Logf("Configured CPU Periods: %d\n", stats.CPUStats.ThrottlingData.Periods)
	t.Logf("Configured CPU Quota (Throttled Time): %d\n", stats.CPUStats.ThrottlingData.ThrottledTime)
	//t.Logf("Container inspect: %v , %v , %v", a, a.Config, a.NetworkSettings)

	// Cleanup
	t.Cleanup(func() {
		client.Stop(context.Background(), testContainerID)
		connection.Close()

	})

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
