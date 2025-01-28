package main

import (
	"flag"
	"log/slog"
	"os"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
)

type WorkerConfig struct {
	General struct {
		Address     string `env:"WORKER_ADDRESS"`
		Environment string `env:"ENVIRONMENT"`
	}
	Runtime struct {
		Type       string `env:"RUNTIME_TYPE"`
		AutoRemove bool   `env:"RUNTIME_AUTOREMOVE"`
	}
	Log struct {
		Level    string `env:"LOG_LEVEL"`
		Format   string `env:"LOG_FORMAT"`
		FilePath string `env:"LOG_FILE"`
	}
}

func parseArgs() (wc WorkerConfig) {
	flag.StringVar(&(wc.General.Address), "address", "", "Worker address. (Env: WORKER_ADDRESS)")
	flag.StringVar(&(wc.Runtime.Type), "runtime", "", "Container runtime type. (Env: RUNTIME_TYPE)")
	flag.BoolVar(&(wc.Runtime.AutoRemove), "auto-remove", false, "Auto remove containers. (Env: RUNTIME_AUTOREMOVE)")
	flag.StringVar(&(wc.Log.Level), "log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	flag.StringVar(&(wc.Log.Format), "log-format", "text", "Log format (json or text) (Env: LOG_FORMAT)")
	flag.StringVar(&(wc.Log.FilePath), "log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	flag.StringVar(&(wc.General.Environment), "environment", "local", "Specify the environment to run in (local, compose)")

	flag.Parse()
	return
}

func setupLogger(config WorkerConfig) *slog.Logger {
	// Set up log level
	var level slog.Level
	switch config.Log.Level {
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
	if config.Log.FilePath != "" {
		output, err = os.OpenFile(config.Log.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	if config.Log.Format == "json" {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}

	// Create and set logger
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

func main() {
	wc := parseArgs()
	logger := setupLogger(wc)

	logger.Info("Current configuration", "config", wc)

	var runtime cr.ContainerRuntime

	// Runtime
	switch wc.Runtime.Type {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(wc.Runtime.AutoRemove, wc.General.Environment, logger)
	default:
		logger.Error("No runtime specified")
		os.Exit(1)
	}

	c := controller.NewController(runtime, logger, wc.General.Address)

	c.StartServer()
}
