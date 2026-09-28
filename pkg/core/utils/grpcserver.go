package utils

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	_ "net/http/pprof"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// GRPCServerOptions configures a blocking gRPC server with health checks and graceful shutdown.
type GRPCServerOptions struct {
	Logger      *slog.Logger
	ListenAddr  string
	ServiceName string
	Register    func(*grpc.Server)
}

// RunGRPCServer listens, registers services, and blocks until ctx is cancelled.
func RunGRPCServer(ctx context.Context, opts GRPCServerOptions) error {
	if opts.Register == nil {
		return fmt.Errorf("grpc server register callback is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	listener, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", opts.ListenAddr, err)
	}

	grpcServer := grpc.NewServer()

	healthcheck := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthcheck)
	if opts.ServiceName != "" {
		healthcheck.SetServingStatus(opts.ServiceName, healthpb.HealthCheckResponse_SERVING)
	}
	healthcheck.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	opts.Register(grpcServer)

	go func() {
		<-ctx.Done()
		healthcheck.Shutdown()
		grpcServer.GracefulStop()
		_ = listener.Close()
	}()

	opts.Logger.Info("gRPC server ready", "address", listener.Addr(), "service", opts.ServiceName)
	if err := grpcServer.Serve(listener); err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve gRPC: %w", err)
	}
	return ctx.Err()
}

// StartPprof starts an optional pprof HTTP server. Returns a cleanup function.
func StartPprof(ctx context.Context, logger *slog.Logger, address string) func() {
	if address == "" {
		return func() {}
	}

	server := &http.Server{Addr: address, Handler: nil}

	go func() {
		logger.Info("pprof server ready", "address", address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Warn("pprof server stopped", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	return func() { _ = server.Close() }
}
