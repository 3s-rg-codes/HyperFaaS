package functionruntime

import (
	"log/slog"
	"net"
	"net/http"
	"os"
)

// HTTP hosts a user HTTP handler on port 50052 and signals readiness to the worker.
type HTTP struct {
	settings settings
	logger   *slog.Logger
}

func NewHTTP() *HTTP {
	return &HTTP{settings: loadSettings()}
}

func (f *HTTP) Ready(handler http.Handler) {
	if handler == nil {
		panic("functionruntime: handler must not be nil")
	}
	if f.settings.controllerAddress == "" {
		panic("functionruntime: CONTROLLER_ADDRESS is required")
	}
	if f.settings.instanceID == 0 {
		panic("functionruntime: INSTANCE_ID is required")
	}

	f.logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

	lis, err := net.Listen("tcp", "0.0.0.0:"+f.settings.functionPort)
	if err != nil {
		f.logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	go notifyControllerReady(f.settings.controllerAddress, f.settings.instanceID, f.logger)

	f.logger.Info("HTTP server starting", "port", f.settings.functionPort)
	if err := http.Serve(lis, handler); err != nil {
		f.logger.Error("failed to serve", "error", err)
		os.Exit(1)
	}
}
