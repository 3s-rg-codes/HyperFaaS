package grpcproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"

	"github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

// LeafResolver selects a leaf proxy address for a function ID.
type LeafResolver interface {
	ResolveLeafProxy(ctx context.Context, functionID uint64) (leafID uint64, proxyAddress string, err error)
}

// LeafSelector selects a leaf for one request. *routing.Engine implements it.
type LeafSelector interface {
	Pick(routing.RouteRequest) (routing.LeafTarget, error)
}

type routingResolver struct {
	selector   LeafSelector
	proxyAddrs map[uint64]string
}

// LeafProxyAddrs maps leaf IDs to transparent proxy listen addresses.
type LeafProxyAddrs map[uint64]string

// NewRoutingResolver builds a resolver backed by the routing engine. The active
// policy lives inside the engine, so the resolver holds only the selector.
func NewRoutingResolver(
	selector LeafSelector,
	proxyAddrs LeafProxyAddrs,
) LeafResolver {
	addrs := make(map[uint64]string, len(proxyAddrs))
	for id, addr := range proxyAddrs {
		addrs[id] = addr
	}
	return &routingResolver{selector: selector, proxyAddrs: addrs}
}

func (r *routingResolver) ResolveLeafProxy(_ context.Context, functionID uint64) (uint64, string, error) {
	target, err := r.selector.Pick(routing.RouteRequest{FunctionID: functionID})
	if err != nil {
		return 0, "", err
	}
	addr, ok := r.proxyAddrs[target.LeafID]
	if !ok {
		return 0, "", fmt.Errorf("leaf %d: grpc_proxy_address not configured", target.LeafID)
	}
	return target.LeafID, addr, nil
}

// Server hosts the ingress transparent gRPC proxy.
type Server struct {
	listenAddr string
	resolver   LeafResolver
	pool       *ConnPool
	logger     *slog.Logger
}

func NewServer(listenAddr string, resolver LeafResolver, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		listenAddr: listenAddr,
		resolver:   resolver,
		pool:       NewConnPool(),
		logger:     logger,
	}
}

func (s *Server) Run(ctx context.Context) error {
	director := func(ctx context.Context, fullMethodName string) (proxy.Mode, []proxy.Backend, error) {
		_ = fullMethodName
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return proxy.One2One, nil, fmt.Errorf("missing metadata")
		}
		functionID, err := FunctionIDFromMetadata(ctx, md)
		if err != nil {
			return proxy.One2One, nil, err
		}
		outMD := md.Copy()
		outMD.Set(MetadataFunctionID, strconv.FormatUint(functionID, 10))
		_, proxyAddr, err := s.resolver.ResolveLeafProxy(ctx, functionID)
		if err != nil {
			return proxy.One2One, nil, err
		}
		return proxy.One2One, []proxy.Backend{&leafProxyBackend{
			address: proxyAddr,
			pool:    s.pool,
			md:      outMD,
		}}, nil
	}

	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("listen transparent gRPC proxy on %q: %w", s.listenAddr, err)
	}

	grpcServer := grpc.NewServer(
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	)

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
		_ = listener.Close()
		s.pool.Close()
	}()

	s.logger.Info("transparent gRPC proxy ready", "address", listener.Addr())
	if err := grpcServer.Serve(listener); err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve transparent gRPC proxy on %q: %w", s.listenAddr, err)
	}
	return ctx.Err()
}

type leafProxyBackend struct {
	address string
	pool    *ConnPool
	md      metadata.MD
}

func (b *leafProxyBackend) String() string {
	return b.address
}

func (b *leafProxyBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	conn, err := b.pool.Get(ctx, b.address)
	if err != nil {
		return nil, nil, err
	}
	outMD := b.md.Copy()
	if vals := b.md.Get(":authority"); len(vals) > 0 {
		outMD.Set(":authority", vals[0])
	}
	outgoing := metadata.NewOutgoingContext(ctx, outMD)
	return outgoing, conn, nil
}

func (b *leafProxyBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) { return resp, nil }

func (b *leafProxyBackend) BuildError(bool, error) ([]byte, error) { return nil, nil }
