package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"testing"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	helpers "github.com/3s-rg-codes/HyperFaaS/test_helpers"
)

var (
	controllerServerAddress = flag.String("ServerAddress", helpers.SERVER_ADDRESS, "specify controller server address")
	requestedRuntime        = flag.String("specifyRuntime", helpers.RUNTIME, "for now only docker, is also default")
	autoRemove              = flag.Bool("autoRemove", true, "specify if containers should be removed after stopping")
)

var runtime *dockerRuntime.DockerRuntime

// Container CPU and Memory configuration is OS dependent. Currently the CPU and Memory configurations are implemented for our Docker containers,
// but it is not clear how to verify the configurations. This test starts a container and prints the configurations obtained from ContainerStats.
func TestContainerConfig(t *testing.T) {

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

	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Fatalf("Failed to build client: %v", err)
	}

	testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: helpers.ImageTags[0]}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *helpers.CPUPeriod, Quota: *helpers.CPUQuota}, Memory: helpers.MemoryLimit}})

	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	//_, _ := runtime.Cli.ContainerInspect(context.Background(), testContainerID.Id)

	cs := caller.New()

	switch *requestedRuntime {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(*autoRemove, cs) //did not work otherwise, using container runtime interface
	}

	st, _ := runtime.Cli.ContainerStats(context.Background(), testContainerID.Id, false)

	buf := new(bytes.Buffer)
	buf.ReadFrom(st.Body)
	respString := buf.String()
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
