package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hyperfaas-ideal-arch/pkg/core/utils"
	"hyperfaas-ideal-arch/pkg/ingress"
)

func main() {
	cfgPath := utils.ConfigPath("configs/ingress.yaml")

	cfg, err := ingress.LoadConfig(cfgPath)
	if err != nil {
		os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := utils.SetupLogger(cfg.Logging).With("node_id", cfg.NodeID, "component", "ingress")
	logger.Info("starting ingress",
		"config", cfgPath,
		"http", cfg.Server.HTTPAddress,
		"grpc_proxy", cfg.Server.GRPCProxyAddress,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := ingress.Bootstrap(ctx, cfg, logger)
	if err != nil {
		logger.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}
	rt.StartWatchers(ctx)

	srv, err := ingress.NewServer(cfg, logger,
		ingress.WithRouting(rt.Engine),
		ingress.WithGRPCProxy(ingress.NewGRPCProxyFromRuntime(cfg, rt, logger)),
	)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
