package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/api"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

type workerIDs []string

func (i *workerIDs) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *workerIDs) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// start the leader

// handle configuration
// scrape intervals, batching of requests to the scheduler, etc.

func main() {
	workerIDs := workerIDs{}
	scrapeInterval := flag.Duration("scrape-interval", 50*time.Millisecond, "The interval at which to scrape the workers")
	address := flag.String("address", "0.0.0.0:50050", "The address to listen on")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	logFormat := flag.String("log-format", "text", "Log format (json or text) (Env: LOG_FORMAT)")
	logFilePath := flag.String("log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	flag.Var(&workerIDs, "worker-ids", "The IDs of the workers to manage")
	flag.Parse()

	if len(workerIDs) == 0 {
		panic("no worker IDs provided")
	}

	logger := setupLogger(*logLevel, *logFormat, *logFilePath)

	scraper := state.NewScraper(*scrapeInterval, logger)

	// For now, we just set these once. In the future, we will have a way to update the worker IPs if needed.
	var ids []state.WorkerID
	for _, id := range workerIDs {
		ids = append(ids, state.WorkerID(id))
	}
	scraper.SetWorkerIDs(ids)

	workerState := make(state.WorkerStateMap)

	scheduler := scheduling.NewNaiveScheduler(workerState, ids, logger)
	server := api.NewLeafServer(scheduler)

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
	if logFormat == "json" {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}

	// Create and set logger
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}
