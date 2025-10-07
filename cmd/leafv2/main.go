package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leafv2"
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

type addressList []string

func (a *addressList) String() string {
	return strings.Join(*a, ",")
}

func (a *addressList) Set(value string) error {
	*a = append(*a, value)
	return nil
}

func main() {
	var workerAddrs addressList

	address := flag.String("address", "0.0.0.0:50050", "Leaf listen address")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json, dev)")
	logFile := flag.String("log-file", "", "Optional log file path")

	scaleToZeroAfter := flag.Duration("scale-to-zero-after", 90*time.Second, "Duration of inactivity before scaling to zero")
	maxInstancesPerWorker := flag.Int("max-instances-per-worker", 4, "Maximum warm instances per worker for a function")
	dialTimeout := flag.Duration("dial-timeout", 5*time.Second, "Worker dial timeout")
	startTimeout := flag.Duration("start-timeout", 45*time.Second, "Worker start timeout")
	stopTimeout := flag.Duration("stop-timeout", 10*time.Second, "Worker stop timeout")
	callTimeout := flag.Duration("call-timeout", 20*time.Second, "Worker call timeout")
	statusBackoff := flag.Duration("status-backoff", 2*time.Second, "Backoff applied when worker status stream fails")

	flag.Var(&workerAddrs, "worker-addr", "Worker gRPC address (repeat for multiple workers)")

	flag.Parse()

	if len(workerAddrs) == 0 {
		fmt.Fprintln(os.Stderr, "at least one --worker-addr must be provided")
		os.Exit(1)
	}

	logger := utils.SetupLogger(*logLevel, *logFormat, *logFile)
	logger.Info("starting LeafV2",
		"address", *address,
		"workers", workerAddrs,
		"scale_to_zero_after", scaleToZeroAfter.String(),
		"max_instances_per_worker", *maxInstancesPerWorker,
	)

	cfg := leafv2.Config{
		WorkerAddresses:       append([]string(nil), workerAddrs...),
		ScaleToZeroAfter:      *scaleToZeroAfter,
		MaxInstancesPerWorker: *maxInstancesPerWorker,
		DialTimeout:           *dialTimeout,
		StartTimeout:          *startTimeout,
		StopTimeout:           *stopTimeout,
		CallTimeout:           *callTimeout,
		StatusBackoff:         *statusBackoff,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := leafv2.NewServer(sigCtx, cfg, logger)
	if err != nil {
		logger.Error("failed to build leaf server", "error", err)
		os.Exit(1)
	}
	defer func() {
		if cerr := server.Close(); cerr != nil {
			logger.Warn("error while closing server", "error", cerr)
		}
	}()

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(utils.InterceptorLogger(logger)),
	)

	leafpb.RegisterLeafServer(grpcServer, server)

	logger.Info("leaf server ready", "address", listener.Addr())

	if err := grpcServer.Serve(listener); err != nil {
		logger.Error("gRPC server stopped", "error", err)
	}
}
