package functionRuntimeInterface

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
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
	timeout      int
	address      string
	request      *Request
	response     *Response
	instanceId   string
	functionId   string
	handler      func(context.Context, *common.CallRequest) (*common.CallResponse, error)
	lastActivity time.Time
	activityMu   sync.RWMutex
	server       *grpc.Server
	functionpb.UnimplementedFunctionServiceServer
}

func New(timeout int) *Function {
	address, ok := os.LookupEnv("CALLER_SERVER_ADDRESS")
	if !ok {
		fmt.Printf("Environment variable CALLER_SERVER_ADDRESS not found")
	}
	functionId, ok := os.LookupEnv("FUNCTION_ID")
	if !ok {
		fmt.Printf("Environment variable FUNCTION_ID not found")
	}
	fmt.Printf("CALLER_SERVER_ADDRESS: %s", address)

	return &Function{
		timeout:    timeout,
		address:    address,
		request:    &Request{},
		response:   &Response{},
		instanceId: getID(),
		functionId: functionId,
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

	f.server = grpc.NewServer()
	f.lastActivity = time.Now()

	functionpb.RegisterFunctionServiceServer(f.server, f)

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	go f.monitorTimeout(logger)

	logger.Info("Server starting", "timeout", f.timeout)
	f.server.Serve(lis)
}

func (f *Function) monitorTimeout(logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)

	for range ticker.C {
		f.activityMu.RLock()
		timeSinceLastActivity := time.Since(f.lastActivity)
		f.activityMu.RUnlock()

		if timeSinceLastActivity >= time.Duration(f.timeout)*time.Second {
			logger.Info("Server timeout reached, shutting down",
				"timeout", f.timeout,
				"last_activity", timeSinceLastActivity)

			f.server.GracefulStop()
			return
		}
	}
}

func configLog(logFile string) *slog.Logger {
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)

	if err != nil {
		console := slog.New(slog.NewTextHandler(os.Stdout, nil))
		console.Error("Failed to create log file, using stdout", "error", err)
		return console
	}

	return slog.New(slog.NewTextHandler(file, nil))
}

func getID() string {
	var id string
	file, err := os.Open(".env")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CONTAINER_ID=") {
			id = strings.TrimPrefix(line, "CONTAINER_ID=")
			break
		}
	}

	if err := scanner.Err(); err != nil {
		panic(err)
	}
	return id
}
