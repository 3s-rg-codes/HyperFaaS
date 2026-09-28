package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hyperfaas-ideal-arch/pkg/core/utils"
	"hyperfaas-ideal-arch/pkg/worker"
)

func main() {
	cfgPath := utils.ConfigPath("configs/worker.yaml")

	cfg, err := worker.LoadConfig(cfgPath)
	if err != nil {
		os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := utils.SetupLogger(cfg.Logging).With("node_id", cfg.NodeID, "component", "worker")
	logger.Info("starting worker",
		"config", cfgPath,
		"listen", cfg.Server.ListenAddress,
		"runtime", cfg.Runtime.Type,
	)

	srv, err := worker.NewServer(cfg, logger)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
