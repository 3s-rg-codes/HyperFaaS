package helpers

import (
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"log/slog"
)

const (
	SERVER_ADDRESS = "localhost:50051"
	RUNTIME        = "docker"
	ENVIRONMENT    = "local"
)

var (
	ImageTags      = []string{"hyperfaas-hello:latest", "hyperfaas-crash:latest", "hyperfaas-echo:latest", "hyperfaas-sleep:latest"}
	TestController *controller.Controller
	Logger         *slog.Logger
)
