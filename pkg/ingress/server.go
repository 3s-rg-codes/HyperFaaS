package ingress

import (
	"context"
	"fmt"
	"log/slog"

	"hyperfaas-ideal-arch/pkg/core/utils"
	"hyperfaas-ideal-arch/pkg/ingress/grpcproxy"
	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

// LeafSelector selects a leaf for one request. *routing.Engine implements it.
// The interface exists so the request path does not depend on the controller.
type LeafSelector interface {
	Pick(routing.RouteRequest) (routing.LeafTarget, error)
}

// IngressServer hosts public HTTP invoke and transparent gRPC proxy listeners.
type IngressServer struct {
	cfg       IngressConfig
	logger    *slog.Logger
	selector  LeafSelector
	grpcProxy *grpcproxy.Server
}

type ingressServerOption func(*IngressServer)

// WithRouting injects the routing entry point. The active policy lives inside
// the engine, so no separate router is needed.
func WithRouting(selector LeafSelector) ingressServerOption {
	return func(s *IngressServer) {
		s.selector = selector
	}
}

// WithGRPCProxy injects the transparent gRPC proxy server.
func WithGRPCProxy(srv *grpcproxy.Server) ingressServerOption {
	return func(s *IngressServer) {
		s.grpcProxy = srv
	}
}

// NewServer builds an ingress server from configuration.
func NewServer(cfg IngressConfig, logger *slog.Logger, opts ...ingressServerOption) (*IngressServer, error) {
	if logger == nil {
		return nil, fmt.Errorf("ingress: logger is required")
	}
	s := &IngressServer{cfg: cfg, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Run starts pprof (if configured), HTTP invoke, and transparent gRPC proxy listeners.
func (s *IngressServer) Run(ctx context.Context) error {
	stopPprof := utils.StartPprof(ctx, s.logger, s.cfg.Server.PprofAddress)
	defer stopPprof()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		errCh <- utils.RunHTTPServer(ctx, s.logger, s.cfg.Server.HTTPAddress,
			newHTTPInvokeHandler(s.logger, s.selector, s.cfg.Routing.LeafTransport))
	}()

	if s.grpcProxy != nil {
		go func() {
			errCh <- s.grpcProxy.Run(ctx)
		}()
	}

	pending := 2
	if s.grpcProxy == nil {
		pending = 1
	}

	select {
	case <-ctx.Done():
		for range pending {
			<-errCh
		}
		return ctx.Err()
	case err := <-errCh:
		cancel()
		for i := 1; i < pending; i++ {
			<-errCh
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
		return ctx.Err()
	}
}

// NewGRPCProxyFromRuntime builds the transparent gRPC proxy for a bootstrapped runtime.
func NewGRPCProxyFromRuntime(cfg IngressConfig, rt *Runtime, logger *slog.Logger) *grpcproxy.Server {
	addrs := make(grpcproxy.LeafProxyAddrs, len(cfg.Leaves))
	for _, leaf := range cfg.Leaves {
		addrs[leaf.ID] = leaf.GRPCProxyAddress
	}
	resolver := grpcproxy.NewRoutingResolver(rt.Engine, addrs)
	return grpcproxy.NewServer(cfg.Server.GRPCProxyAddress, resolver, logger)
}
