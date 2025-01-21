package helpers

import (
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"
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
	ENVIRONMENT    = "local"
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
