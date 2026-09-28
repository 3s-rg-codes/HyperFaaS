package grpcproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
)

// LeaseInvoker leases capacity for proxied gRPC calls.
type LeaseInvoker interface {
	LeaseForProxy(ctx context.Context, functionID uint64) (*dataplane.Lease, *grpc.ClientConn, error)
}

// Run starts the transparent gRPC proxy listener until ctx is cancelled.
func Run(ctx context.Context, invoker LeaseInvoker, cfg leaf.LeafConfig, logger *slog.Logger) error {
	if cfg.GRPCProxy.ListenAddress == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	director := func(ctx context.Context, fullMethodName string) (proxy.Mode, []proxy.Backend, error) {
		_ = fullMethodName
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return proxy.One2One, nil, fmt.Errorf("missing metadata")
		}
		functionID, err := FunctionIDFromAuthority(ctx, md)
		if err != nil {
			return proxy.One2One, nil, err
		}
		return proxy.One2One, []proxy.Backend{&leasingBackend{
			functionID: functionID,
			invoker:    invoker,
			md:         md,
		}}, nil
	}

	listener, err := net.Listen("tcp", cfg.GRPCProxy.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen grpc proxy: %w", err)
	}

	grpcServer := grpc.NewServer(
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	)

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
		_ = listener.Close()
	}()

	logger.Info("gRPC proxy ready", "address", listener.Addr())
	if err := grpcServer.Serve(listener); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

type leasingBackend struct {
	functionID uint64
	invoker    LeaseInvoker
	md         metadata.MD
}

func (b *leasingBackend) String() string {
	return fmt.Sprintf("function-%d", b.functionID)
}

func (b *leasingBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	lease, conn, err := b.invoker.LeaseForProxy(ctx, b.functionID)
	if err != nil {
		return nil, nil, err
	}
	outCtx, cancel := context.WithCancel(ctx)
	go func() {
		<-outCtx.Done()
		lease.Release()
		cancel()
	}()
	outgoing := metadata.NewOutgoingContext(outCtx, b.md.Copy())
	return outgoing, conn, nil
}

func (b *leasingBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) { return resp, nil }

func (b *leasingBackend) BuildError(bool, error) ([]byte, error) { return nil, nil }
