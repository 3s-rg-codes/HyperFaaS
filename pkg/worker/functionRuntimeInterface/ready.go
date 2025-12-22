package functionRuntimeInterface

import (
	"context"
	"log/slog"
	"os"

	workerPB "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func notifyControllerReady(controllerAddress, instanceId string, logger *slog.Logger) {
	conn, err := grpc.NewClient(controllerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Error("Failed to connect to controller", "error", err)
		os.Exit(1)
	}

	client := workerPB.NewWorkerClient(conn)
	if _, err = client.SignalReady(context.Background(), &workerPB.SignalReadyRequest{InstanceId: instanceId}); err != nil {
		logger.Error("Failed to send ready signal", "error", err)
		os.Exit(1)
	}
	logger.Info("Ready signal sent")
}
