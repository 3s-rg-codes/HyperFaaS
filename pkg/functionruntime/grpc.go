package functionruntime

import (
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
)

// GRPC hosts a user gRPC service on port 50052 and signals readiness to the worker.
type GRPC struct {
	settings   settings
	logger     *slog.Logger
	server     *grpc.Server
	serverOpts []grpc.ServerOption
}

func NewGRPC(opts ...grpc.ServerOption) *GRPC {
	return &GRPC{
		settings:   loadSettings(),
		serverOpts: opts,
	}
}

func (f *GRPC) Ready(register func(grpc.ServiceRegistrar)) {
	if register == nil {
		panic("functionruntime: register func must not be nil")
	}
	if f.settings.controllerAddress == "" {
		panic("functionruntime: CONTROLLER_ADDRESS is required")
	}
	if f.settings.instanceID == 0 {
		panic("functionruntime: INSTANCE_ID is required")
	}

	f.logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	f.server = grpc.NewServer(f.serverOpts...)
	register(f.server)

	lis, err := net.Listen("tcp", "0.0.0.0:"+f.settings.functionPort)
	if err != nil {
		f.logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	go notifyControllerReady(f.settings.controllerAddress, f.settings.instanceID, f.logger)

	f.logger.Info("gRPC server starting", "port", f.settings.functionPort)
	if err := f.server.Serve(lis); err != nil {
		f.logger.Error("failed to serve", "error", err)
		os.Exit(1)
	}
}
