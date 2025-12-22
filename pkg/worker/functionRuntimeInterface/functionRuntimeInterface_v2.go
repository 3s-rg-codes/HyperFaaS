package functionRuntimeInterface

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
)

// FunctionV2 allows users to create their own gRPC services with their own proto files.
type FunctionV2 struct {
	timeoutSeconds    int32
	controllerAddress string
	instanceId        string
	functionId        string

	logger *slog.Logger

	server       *grpc.Server
	serverOpts   []grpc.ServerOption
	lastActivity time.Time
	activityMu   sync.RWMutex
}

func NewV2(timeout int, opts ...grpc.ServerOption) *FunctionV2 {
	settings := loadRuntimeSettings()
	fn := &FunctionV2{
		timeoutSeconds:    settings.timeoutSeconds,
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
	f.updateActivity()

	register(f.server)

	lis, err := net.Listen("tcp", "0.0.0.0:50052")
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	go f.monitorTimeout()

	logger.Info("Sending ready signal to controller")

	go f.sendReadySignal()

	logger.Info("User gRPC server starting", "timeout", f.timeoutSeconds)

	if serveErr := f.server.Serve(lis); serveErr != nil {
		logger.Error("Failed to serve", "error", serveErr)
		os.Exit(1)
	}
}

func (f *FunctionV2) buildServerOptions() []grpc.ServerOption {
	options := []grpc.ServerOption{
		// for tracking activity
		grpc.ChainUnaryInterceptor(f.unaryActivityInterceptor),
	}
	return append(options, f.serverOpts...)
}

func (f *FunctionV2) unaryActivityInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	f.updateActivity()
	return handler(ctx, req)
}

func (f *FunctionV2) updateActivity() {
	f.activityMu.Lock()
	f.lastActivity = time.Now()
	f.activityMu.Unlock()
}

func (f *FunctionV2) monitorTimeout() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		f.activityMu.RLock()
		inactive := time.Since(f.lastActivity)
		f.activityMu.RUnlock()

		if inactive >= time.Duration(f.timeoutSeconds)*time.Second {
			if f.logger != nil {
				f.logger.Info("Server timeout reached, shutting down",
					"timeout", f.timeoutSeconds,
					"last_activity", inactive)
			}
			f.server.GracefulStop()
			return
		}
	}
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
