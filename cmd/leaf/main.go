package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/api"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
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
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	workerIDs := workerIDs{}
	address := flag.String("address", "0.0.0.0:50050", "The address to listen on")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	logFormat := flag.String("log-format", "text", "Log format (json, text or dev) (Env: LOG_FORMAT)")
	logFilePath := flag.String("log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	databaseType := flag.String("database-type", "http", "\"database\" used for managing the functionID -> config relationship")
	databaseAddress := flag.String("database-address", "http://localhost:8999/", "address of the database server")
	maxStartingInstancesPerFunction := flag.Int("max-starting-instances-per-function", 10, "The maximum number of instances starting at once per function")
	startingInstanceWaitTimeout := flag.Duration("starting-instance-wait-timeout", time.Second*5, "The timeout for waiting for an instance to start")
	maxRunningInstancesPerFunction := flag.Int("max-running-instances-per-function", 10, "The maximum number of instances running at once per function")
	panicBackoff := flag.Duration("panic-backoff", time.Millisecond*50, "The starting backoff time for the panic mode")
	panicBackoffIncrease := flag.Duration("panic-backoff-increase", time.Millisecond*50, "The backoff increase for the panic mode")
	panicMaxBackoff := flag.Duration("panic-max-backoff", time.Second*1, "The maximum backoff for the panic mode")
	flag.Var(&workerIDs, "worker-ids", "The IDs of the workers to manage")
	flag.Parse()

	if len(workerIDs) == 0 {
		panic("no worker IDs provided")
	}

	logger := utils.SetupLogger(*logLevel, *logFormat, *logFilePath)

	// Print configuration
	logger.Info("Configuration", "address", *address, "logLevel", *logLevel, "logFormat", *logFormat, "logFilePath", *logFilePath, "databaseType", *databaseType, "databaseAddress", *databaseAddress, "workerIDs", workerIDs)

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
		dbClient = keyValueStore.NewHttpDBClient(*databaseAddress, logger)
	}

	leafConfig := config.LeafConfig{
		MaxStartingInstancesPerFunction: *maxStartingInstancesPerFunction,
		StartingInstanceWaitTimeout:     *startingInstanceWaitTimeout,
		MaxRunningInstancesPerFunction:  *maxRunningInstancesPerFunction,
		PanicBackoff:                    *panicBackoff,
		PanicBackoffIncrease:            *panicBackoffIncrease,
		PanicMaxBackoff:                 *panicMaxBackoff,
	}

	server := api.NewLeafServer(leafConfig, dbClient, ids, logger)

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(utils.InterceptorLogger(logger)),
	)
	pb.RegisterLeafServer(grpcServer, server)
	logger.Info("Leaf server started", "address", listener.Addr())
	if err := grpcServer.Serve(listener); err != nil {
		logger.Error("failed to serve", "error", err)
		os.Exit(1)
	}
}

//nolint:all
func healthCheckWorker(workerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This function is deprecated but I'm not sure how to replace it
	// https://github.com/grpc/grpc-go/blob/master/Documentation/anti-patterns.md
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
