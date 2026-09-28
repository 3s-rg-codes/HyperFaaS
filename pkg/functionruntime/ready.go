package functionruntime

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

func notifyControllerReady(controllerAddress string, instanceID uint64, logger *slog.Logger) {
	// This goroutine can be captured in a Firecracker snapshot. A stale
	// notification after restore must not terminate the function process.
	conn, err := grpc.NewClient(controllerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Error("failed to connect to worker", "error", err)
		return
	}
	defer conn.Close()

	client := workerpb.NewSandboxServiceClient(conn)
	if _, err = client.SignalReady(context.Background(), &workerpb.SignalReadyRequest{InstanceId: instanceID}); err != nil {
		logger.Error("failed to send ready signal", "error", err)
		return
	}
	logger.Info("ready signal sent", "instance_id", instanceID)
}
