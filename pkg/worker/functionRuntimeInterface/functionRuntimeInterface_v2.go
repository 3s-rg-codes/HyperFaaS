package functionRuntimeInterface

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
)

// FunctionV2 allows users to create their own gRPC services with their own proto files.
type FunctionV2 struct {
	controllerAddress string
	instanceId        string
	functionId        string

	logger *slog.Logger

	server     *grpc.Server
	serverOpts []grpc.ServerOption
}

func NewV2(opts ...grpc.ServerOption) *FunctionV2 {
	settings := loadRuntimeSettings()
	fn := &FunctionV2{
		controllerAddress: settings.controllerAddress,
		instanceId:        getID(),
		functionId:        settings.functionID,
		serverOpts:        opts,
	}
	fn.server = grpc.NewServer(fn.buildServerOptions()...)
	return fn
}

func (f *FunctionV2) Ready(register func(grpc.ServiceRegistrar)) {
	if register == nil {
		panic("functionRuntimeInterface: register func must not be nil")
	}

	logger := configLog(
		fmt.Sprintf("/logs/%s-%s.log", time.Now().Format("2006-01-02-15-04-05"), f.instanceId),
	)
	f.logger = logger

	register(f.server)

	lis, err := net.Listen("tcp", "0.0.0.0:50052")
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	logger.Info("Sending ready signal to controller")

	go f.sendReadySignal()

	logger.Info("User gRPC server starting")

	if serveErr := f.server.Serve(lis); serveErr != nil {
		logger.Error("Failed to serve", "error", serveErr)
		os.Exit(1)
	}
}

func (f *FunctionV2) buildServerOptions() []grpc.ServerOption {
	return append([]grpc.ServerOption{}, f.serverOpts...)
}

func (f *FunctionV2) sendReadySignal() {
	if f.logger == nil {
		return
	}
	notifyControllerReady(f.controllerAddress, f.instanceId, f.logger)
}

func configLog(logFile string) *slog.Logger {
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		console := slog.New(slog.NewTextHandler(os.Stdout, nil))
		console.Error("Failed to create log file, using stdout", "error", err)
		return console
	}

	return slog.New(slog.NewTextHandler(file, nil))
}

func getID() string {
	hostname, err := os.Hostname()
	if err != nil {
		panic(fmt.Sprintf("Failed to get hostname: %v", err))
	}
	return hostname
}
