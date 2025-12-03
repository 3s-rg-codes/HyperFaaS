package functionRuntimeInterface

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	functionpb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
)

type Request struct {
	Data []byte
	Id   string
}

type Response struct {
	Data  []byte
	Error string
	Id    string
}

type Function struct {
	timeoutSeconds    int32
	controllerAddress string
	request           *Request
	response          *Response
	instanceId        string
	functionId        string
	handler           func(context.Context, *common.CallRequest) (*common.CallResponse, error)
	lastActivity      time.Time
	activityMu        sync.RWMutex
	server            *grpc.Server
	logger            *slog.Logger
	functionpb.UnimplementedFunctionServiceServer
}

func New(timeout int) *Function {
	settings := loadRuntimeSettings()

	return &Function{
		timeoutSeconds:    settings.timeoutSeconds,
		controllerAddress: settings.controllerAddress,
		request:           &Request{},
		response:          &Response{},
		instanceId:        getID(),
		functionId:        settings.functionID,
	}
}

func (f *Function) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	f.activityMu.Lock()
	f.lastActivity = time.Now()
	f.activityMu.Unlock()

	return f.handler(ctx, req)
}

func (f *Function) Ready(handler func(context.Context, *common.CallRequest) (*common.CallResponse, error)) {
	logger := configLog(fmt.Sprintf("/logs/%s-%s.log", time.Now().Format("2006-01-02-15-04-05"), f.instanceId))
	f.handler = handler
	f.logger = logger

	f.server = grpc.NewServer()
	f.lastActivity = time.Now()

	functionpb.RegisterFunctionServiceServer(f.server, f)

	lis, err := net.Listen("tcp", "0.0.0.0:50052")
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	go f.monitorTimeout(logger)

	logger.Info("Server starting", "timeout", f.timeoutSeconds)

	// Notify controller that the function is ready to serve requests
	go f.sendReadySignal()
	err = f.server.Serve(lis)
	if err != nil {
		logger.Error("Failed to serve", "error", err)
		os.Exit(1)
	}
}

func (f *Function) monitorTimeout(logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)

	for range ticker.C {
		f.activityMu.RLock()
		timeSinceLastActivity := time.Since(f.lastActivity)
		f.activityMu.RUnlock()

		if timeSinceLastActivity >= time.Duration(f.timeoutSeconds)*time.Second {
			logger.Info("Server timeout reached, shutting down",
				"timeout", f.timeoutSeconds,
				"last_activity", timeSinceLastActivity)

			f.server.GracefulStop()
			return
		}
	}
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

func (f *Function) sendReadySignal() {
	notifyControllerReady(f.controllerAddress, f.instanceId, f.logger)
}
