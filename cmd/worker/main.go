package main

import (
	"flag"
	"log"
	"log/slog"
	"os"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/caller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/mock"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"

	"net/http"
	_ "net/http/pprof"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
)

type WorkerConfig struct {
	General struct {
		Address             string `env:"WORKER_ADDRESS"`
		CallerServerAddress string `env:"CALLER_SERVER_ADDRESS"`
		DatabaseType        string `env:"DATABASE_TYPE"`
		ListenerTimeout     int    `env:"LISTENER_TIMEOUT"`
	}
	Runtime struct {
		Type          string `env:"RUNTIME_TYPE"`
		AutoRemove    bool   `env:"RUNTIME_AUTOREMOVE"`
		Containerized bool   `env:"RUNTIME_CONTAINERIZED"`
	}
	Log struct {
		Level    string `env:"LOG_LEVEL"`
		Format   string `env:"LOG_FORMAT"`
		FilePath string `env:"LOG_FILE"`
	}
}

func parseArgs() (wc WorkerConfig) {
	flag.StringVar(&(wc.General.Address), "address", "", "Worker address. (Env: WORKER_ADDRESS)")
	flag.StringVar(&(wc.General.CallerServerAddress), "caller-server-address", "", "Caller server address. (Env: CALLER_SERVER_ADDRESS)")
	flag.StringVar(&(wc.General.DatabaseType), "database-type", "", "Type of the database. (Env: DATABASE_TYPE)")
	flag.StringVar(&(wc.Runtime.Type), "runtime", "docker", "Container runtime type. (Env: RUNTIME_TYPE)")
	flag.IntVar(&(wc.General.ListenerTimeout), "timeout", 20, "Timeout in seconds before leafnode listeners are removed from status stream updates. (Env: LISTENER_TIMEOUT)")
	flag.BoolVar(&(wc.Runtime.AutoRemove), "auto-remove", false, "Auto remove containers. (Env: RUNTIME_AUTOREMOVE)")
	flag.StringVar(&(wc.Log.Level), "log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	flag.StringVar(&(wc.Log.Format), "log-format", "text", "Log format (json or text) (Env: LOG_FORMAT)")
	flag.StringVar(&(wc.Log.FilePath), "log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	flag.BoolVar(&(wc.Runtime.Containerized), "containerized", false, "Use socket to connect to Docker. (Env: RUNTIME_CONTAINERIZED)")

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
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	wc := parseArgs()
	logger := setupLogger(wc)

	logger.Info("Current configuration", "config", wc)

	var runtime cr.ContainerRuntime

	statsManager := stats.NewStatsManager(logger, time.Duration(wc.General.ListenerTimeout)*time.Second, 1.0)

	callerServer := caller.NewCallerServer(wc.General.CallerServerAddress, logger, statsManager)

	var dbAddress string
	var dbClient kv.FunctionMetadataStore

	if wc.Runtime.Containerized {
		dbAddress = "http://database:8080/" //needs to have this format for http to work
	} else {
		dbAddress = "localhost:8080"
	}

	switch wc.General.DatabaseType {
	case "http":
		dbClient = kv.NewHttpClient(dbAddress, logger)
	}

	// Runtime
	switch wc.Runtime.Type {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(wc.Runtime.Containerized, wc.Runtime.AutoRemove, wc.General.CallerServerAddress, logger)
	case "fake":
		runtime = mock.NewMockRuntime(callerServer, logger)
	default:
		logger.Error("No runtime specified")
		os.Exit(1)
	}

	c := controller.NewController(runtime, callerServer, statsManager, logger, wc.General.Address, dbClient)

	c.StartServer()
}
