package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

func main() {
	cfgPath := utils.ConfigPath("configs/controlplane.yaml")

	cfg, err := controlplane.LoadConfig(cfgPath)
	if err != nil {
		os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := utils.SetupLogger(cfg.Logging).With("node_id", cfg.NodeID, "component", "controlplane")
	logger.Info("starting control plane",
		"config", cfgPath,
		"http", cfg.Server.HTTPAddress,
		"grpc", cfg.Server.GRPCAddress,
		"store", cfg.Store.Type,
	)

	srv, err := controlplane.NewServer(cfg, logger)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}
	defer func() { _ = srv.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
