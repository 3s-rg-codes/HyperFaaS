package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/api"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/golang-cz/devslog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type workerIDs []string

func (i *workerIDs) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *workerIDs) Set(value string) error {
	*i = append(*i, value)
	return nil
}
func main() {
	workerIDs := workerIDs{}
	address := flag.String("address", "0.0.0.0:50050", "The address to listen on")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	logFormat := flag.String("log-format", "text", "Log format (json, text or dev) (Env: LOG_FORMAT)")
	logFilePath := flag.String("log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	databaseType := flag.String("database-type", "http", "\"database\" used for managing the functionID -> config relationship")
	databaseAddress := flag.String("database-address", "http://localhost:8080/", "address of the database server")
	schedulerType := flag.String("scheduler-type", "mru", "The type of scheduler to use (mru or map)")
	flag.Var(&workerIDs, "worker-ids", "The IDs of the workers to manage")
	flag.Parse()

	if len(workerIDs) == 0 {
		panic("no worker IDs provided")
	}

	logger := setupLogger(*logLevel, *logFormat, *logFilePath)

	// Print configuration
	logger.Info("Configuration", "address", *address, "logLevel", *logLevel, "logFormat", *logFormat, "logFilePath", *logFilePath, "databaseType", *databaseType, "databaseAddress", *databaseAddress, "schedulerType", *schedulerType, "workerIDs", workerIDs)

	var ids []state.WorkerID
	logger.Debug("Setting worker IDs", "workerIDs", workerIDs, "len", len(workerIDs))
	for _, id := range workerIDs {
		err := healthCheckWorker(id)
		if err != nil {
			logger.Error("failed to health check worker", "error", err)
			os.Exit(1)
		}
		ids = append(ids, state.WorkerID(id))
	}

	var dbClient keyValueStore.FunctionMetadataStore

	switch *databaseType {
	case "http":
		dbClient = keyValueStore.NewHttpClient(*databaseAddress, logger)
	}

	workerState := state.NewWorkers(logger)

	scheduler := scheduling.New(*schedulerType, workerState, ids, logger)

	server := api.NewLeafServer(scheduler, dbClient)

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterLeafServer(grpcServer, server)
	logger.Info("Leaf server started", "address", listener.Addr())
	if err := grpcServer.Serve(listener); err != nil {
		logger.Error("failed to serve", "error", err)
		os.Exit(1)
	}

}

func setupLogger(logLevel string, logFormat string, logFilePath string) *slog.Logger {
	// Set up log level
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Set up log output
	var output *os.File
	var err error
	if logFilePath != "" {
		output, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			slog.Error("Failed to open log file, falling back to stdout", "error", err)
			output = os.Stdout
		}
	} else {
		output = os.Stdout
	}

	// Set up handler options
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	}

	// Create handler based on format
	var handler slog.Handler
	switch logFormat {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	case "text":
		handler = slog.NewTextHandler(output, opts)
	case "dev":
		// new logger with options
		devOpts := &devslog.Options{
			HandlerOptions:    opts,
			MaxSlicePrintSize: 5,
			SortKeys:          true,
			NewLineAfterLog:   true,
			StringerFormatter: true,
		}
		handler = devslog.NewHandler(output, devOpts)
	}

	// Create and set logger
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

var serviceConfig = `{
	"loadBalancingPolicy": "round_robin",
	"healthCheckConfig": {
		"serviceName": ""
	}
}`

func healthCheckWorker(workerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This function is deprecated but I'm not sure how to replace it
	//https://github.com/grpc/grpc-go/blob/master/Documentation/anti-patterns.md
	conn, err := grpc.DialContext(ctx, workerID,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), // This makes the dial synchronous
	)
	if err != nil {
		return fmt.Errorf("failed to connect to worker %s: %w", workerID, err)
	}
	defer conn.Close()

	return nil
}
