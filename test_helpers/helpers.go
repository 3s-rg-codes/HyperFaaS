package test_helpers

import (
	"flag"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"
	"time"
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

const (
	SERVER_ADDRESS = "localhost:50051"
	RUNTIME        = "docker"
)

var (
	CPUPeriod   = flag.Int64("cpuPeriod", 100000, "CPU period")
	CPUQuota    = flag.Int64("cpuQuota", 50000, "CPU quota")
	MemoryLimit = (*flag.Int64("memoryLimit", 250000000, "Memory limit in MB")) * 1024 * 1024
)

var (
	ImageTags      = []string{"hyperfaas-hello:latest", "hyperfaas-crash:latest", "hyperfaas-echo:latest", "hyperfaas-sleep:latest"}
	TestController *controller.Controller
	Logger         *slog.Logger
)

func BuildMockClient(controllerServerAddress string) (pb.ControllerClient, *grpc.ClientConn, error) {
	var err error
	connection, err := grpc.NewClient(controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)

	return testClient, connection, nil
}

func CreateTestState() state.WorkerStateMap {
	// The MRU instance is instance1 for func1 on worker1
	return state.WorkerStateMap{
		"worker1": []state.FunctionState{
			{FunctionID: "func1", Idle: []state.InstanceState{
				{InstanceID: "instance1",
					TimeSinceLastWork: 1 * time.Second,
					Uptime:            5 * time.Second,
				},
				{InstanceID: "instance2",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            4 * time.Second,
				},
				{InstanceID: "instance3",
					TimeSinceLastWork: 3 * time.Second,
					Uptime:            5 * time.Second,
				},
			},
				Running: []state.InstanceState{
					{InstanceID: "instance4",
						TimeSinceLastWork: 4 * time.Second,
						Uptime:            10 * time.Second,
					},
					{InstanceID: "instance5",
						TimeSinceLastWork: 5 * time.Second,
						Uptime:            10 * time.Second,
					},
				},
			},
		},
		"worker2": []state.FunctionState{
			{FunctionID: "func2", Idle: []state.InstanceState{
				{InstanceID: "instance6",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            5 * time.Second,
				},
				{InstanceID: "instance7",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            4 * time.Second,
				},
			},
				Running: []state.InstanceState{
					{InstanceID: "instance8",
						TimeSinceLastWork: 3 * time.Second,
						Uptime:            5 * time.Second,
					},
					{InstanceID: "instance9",
						TimeSinceLastWork: 4 * time.Second,
						Uptime:            5 * time.Second,
					},
				},
			},
		},
	}
}
