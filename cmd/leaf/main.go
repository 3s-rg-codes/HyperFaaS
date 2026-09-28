package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hyperfaas-ideal-arch/pkg/core/utils"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/grpcproxy"
	"hyperfaas-ideal-arch/pkg/leaf/runtime"
)

func main() {
	cfgPath := utils.ConfigPath("configs/leaf.yaml")

	cfg, err := leaf.LoadConfig(cfgPath)
	if err != nil {
		os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := utils.SetupLogger(cfg.Logging).With("leaf_id", cfg.LeafID, "node_id", cfg.NodeID, "component", "leaf")
	logger.Info("starting leaf",
		"config", cfgPath,
		"listen", cfg.Server.ListenAddress,
		"grpc_proxy", cfg.GRPCProxy.ListenAddress,
		"workers", cfg.WorkerAddresses(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := runtime.NewRuntime(ctx, cfg, nil, logger)
	if err != nil {
		logger.Error("failed to create runtime", "error", err)
		os.Exit(1)
	}
	defer rt.Close()

	srv, err := leaf.NewServer(cfg, logger,
		leaf.WithControlService(rt),
		leaf.WithStateReporter(rt),
		leaf.WithFunctionRegistry(rt),
	)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- grpcproxy.Run(ctx, rt, cfg, logger)
	}()
	go func() {
		errCh <- srv.Run(ctx)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}
	stop()
	<-errCh
	<-errCh
}
