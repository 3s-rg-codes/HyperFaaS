package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/network"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"

	_ "net/http/pprof"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime/mock"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
)

type WorkerConfig struct {
	General struct {
		Address         string `env:"WORKER_ADDRESS"`
		ListenerTimeout int    `env:"LISTENER_TIMEOUT"`
	}
	Metadata struct {
		Endpoints   []string
		Prefix      string
		DialTimeout time.Duration
	}
	Runtime struct {
		Type          string `env:"RUNTIME_TYPE"`
		AutoRemove    bool   `env:"RUNTIME_AUTOREMOVE"`
		Containerized bool   `env:"RUNTIME_CONTAINERIZED"`
		ServiceName   string `env:"RUNTIME_SERVICE_NAME"`
		NetworkName   string `env:"RUNTIME_NETWORK_NAME"`
	}
	Log struct {
		Level    string `env:"LOG_LEVEL"`
		Format   string `env:"LOG_FORMAT"`
		FilePath string `env:"LOG_FILE"`
	}
	Stats struct {
		UpdateBufferSize int64 `env:"UPDATE_BUFFER_SIZE"`
	}
}

func parseArgs() (wc WorkerConfig) {
	var etcdEndpoints utils.StringList
	flag.StringVar(&(wc.General.Address), "address", "", "Worker address. (Env: WORKER_ADDRESS)")
	flag.StringVar(&(wc.Runtime.Type), "runtime", "docker", "Container runtime type. (Env: RUNTIME_TYPE)")
	flag.IntVar(&(wc.General.ListenerTimeout), "timeout", 20, "Timeout in seconds before leafnode listeners are removed from status stream updates. (Env: LISTENER_TIMEOUT)")
	flag.BoolVar(&(wc.Runtime.AutoRemove), "auto-remove", false, "Auto remove containers. (Env: RUNTIME_AUTOREMOVE)")
	flag.StringVar(&(wc.Log.Level), "log-level", "info", "Log level (debug, info, warn, error) (Env: LOG_LEVEL)")
	flag.StringVar(&(wc.Log.Format), "log-format", "text", "Log format (json or text) (Env: LOG_FORMAT)")
	flag.StringVar(&(wc.Log.FilePath), "log-file", "", "Log file path (defaults to stdout) (Env: LOG_FILE)")
	flag.BoolVar(&(wc.Runtime.Containerized), "containerized", false, "Use socket to connect to Docker. (Env: RUNTIME_CONTAINERIZED)")
	flag.Int64Var(&(wc.Stats.UpdateBufferSize), "update-buffer-size", 10000, "Update buffer size. (Env: UPDATE_BUFFER_SIZE)")
	flag.StringVar(&(wc.Runtime.ServiceName), "service-name", "worker", "Docker compose service name. (Env: RUNTIME_SERVICE_NAME)")
	flag.StringVar(&(wc.Runtime.NetworkName), "network-name", "hyperfaas-network", "Docker network name for function containers. (Env: RUNTIME_NETWORK_NAME)")
	flag.Var(&etcdEndpoints, "etcd-endpoint", "Etcd endpoint (can be specified multiple times). Defaults to localhost:2379")
	metadataPrefix := flag.String("metadata-prefix", metadata.DefaultPrefix, "Etcd key prefix for function metadata")
	metadataDialTimeout := flag.Duration("metadata-dial-timeout", metadata.DefaultDialTimeout, "Etcd dial timeout")
	flag.Parse()

	wc.Metadata.Endpoints = append(wc.Metadata.Endpoints, etcdEndpoints...)
	wc.Metadata.Prefix = *metadataPrefix
	wc.Metadata.DialTimeout = *metadataDialTimeout
	return
}

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	wc := parseArgs()
	logger := utils.SetupLogger(wc.Log.Level, wc.Log.Format, wc.Log.FilePath)

	logger.Info("Current configuration", "config", wc)

	statsManager := stats.NewStatsManager(logger, time.Duration(wc.General.ListenerTimeout)*time.Second, 1.0, wc.Stats.UpdateBufferSize)

	var runtime cr.ContainerRuntime
	var router controller.CallRouter
	var readySignals *controller.ReadySignals

	if len(wc.Metadata.Endpoints) == 0 {
		if wc.Runtime.Containerized {
			wc.Metadata.Endpoints = []string{"etcd:2379"}
		} else {
			wc.Metadata.Endpoints = []string{"localhost:2379"}
		}
	}

	metadataClient, err := metadata.NewClient(wc.Metadata.Endpoints, metadata.Options{
		Prefix:      wc.Metadata.Prefix,
		DialTimeout: wc.Metadata.DialTimeout,
	}, logger)
	if err != nil {
		logger.Error("Failed to create metadata client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if cerr := metadataClient.Close(); cerr != nil {
			logger.Warn("Failed to close metadata client", "error", cerr)
		}
	}()

	// Runtime
	switch wc.Runtime.Type {
	case "docker":
		readySignals = controller.NewReadySignals(false)
		runtime = dockerRuntime.NewDockerRuntime(wc.Runtime.Containerized, wc.Runtime.AutoRemove, wc.General.Address, logger, wc.Runtime.ServiceName, wc.Runtime.NetworkName)
		router = network.NewCallRouter(logger)
	case "mock":
		readySignals = controller.NewReadySignals(true)
		runtime = mock.NewMockRuntime(logger, readySignals)
		router = mock.NewMockCallRouter(logger, runtime.(*mock.MockRuntime))
	default:
		logger.Error("No runtime specified")
		os.Exit(1)
	}

	c := controller.NewController(runtime, statsManager, logger, wc.General.Address, metadataClient, router, readySignals)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.StartServer(ctx)
}
