package utils

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
)

// RunHTTPServer listens and blocks until ctx is cancelled, then shuts down gracefully.
func RunHTTPServer(ctx context.Context, logger *slog.Logger, listenAddr string, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("http handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	logger.Info("HTTP server ready", "address", listenAddr)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return ctx.Err()
	}
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return ctx.Err()
}
