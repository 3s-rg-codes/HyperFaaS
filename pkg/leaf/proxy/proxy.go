// Taken from https://github.com/mwitkow/grpc-proxy . Apache 2.0 license.
package proxy

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// NewProxy sets up a simple proxy that forwards all requests to dst.
func NewProxy(dst grpc.ClientConnInterface, opts ...grpc.ServerOption) *grpc.Server {
	opts = append(opts, DefaultProxyOpt(dst))
	// Set up the proxy server and then serve from it like in step one.
	return grpc.NewServer(opts...)
}

// DefaultProxyOpt returns an grpc.UnknownServiceHandler with a DefaultDirector.
func DefaultProxyOpt(cc grpc.ClientConnInterface) grpc.ServerOption {
	return grpc.UnknownServiceHandler(TransparentHandler(DefaultDirector(cc)))
}

// RoutingProxyOpt wires a routing-aware director into the proxy server.
func RoutingProxyOpt(resolver BackendResolver, extractor FunctionIDExtractor) grpc.ServerOption {
	return grpc.UnknownServiceHandler(TransparentHandler(RoutingDirector(resolver, extractor)))
}

// DefaultDirector returns a very simple forwarding StreamDirector that forwards all
// calls.
func DefaultDirector(cc grpc.ClientConnInterface) StreamDirector {
	return func(ctx context.Context, fullMethodName string) (context.Context, grpc.ClientConnInterface, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		ctx = metadata.NewOutgoingContext(ctx, md.Copy())
		return ctx, cc, nil
	}
}
