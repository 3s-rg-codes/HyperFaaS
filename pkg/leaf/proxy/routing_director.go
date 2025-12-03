package proxy

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// BackendResolver knows how to get a gRPC connection for a specific function id.
type BackendResolver interface {
	Resolve(ctx context.Context, functionID string, fullMethodName string) (grpc.ClientConnInterface, error)
}

// BackendResolverFunc lets ordinary functions satisfy BackendResolver.
type BackendResolverFunc func(ctx context.Context, functionID string, fullMethodName string) (grpc.ClientConnInterface, error)

// Resolve implements BackendResolver.
func (f BackendResolverFunc) Resolve(ctx context.Context, functionID string, fullMethodName string) (grpc.ClientConnInterface, error) {
	return f(ctx, functionID, fullMethodName)
}

// FunctionIDExtractor is responsible for pulling a function identifier from inbound metadata.
type FunctionIDExtractor func(ctx context.Context, md metadata.MD) (string, error)

// RoutingDirector creates a StreamDirector that picks the backend connection dynamically per call.
func RoutingDirector(resolver BackendResolver, extractor FunctionIDExtractor) StreamDirector {
	if resolver == nil {
		panic("RoutingDirector: resolver must not be nil")
	}
	if extractor == nil {
		extractor = AuthorityFunctionIDExtractor()
	}
	return func(ctx context.Context, fullMethodName string) (context.Context, grpc.ClientConnInterface, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if md == nil {
			return nil, nil, status.Error(codes.InvalidArgument, "missing metadata on incoming context")
		}
		functionID, err := extractor(ctx, md)
		if err != nil {
			return nil, nil, err
		}
		conn, err := resolver.Resolve(ctx, functionID, fullMethodName)
		if err != nil {
			return nil, nil, err
		}
		outgoing := metadata.NewOutgoingContext(ctx, md.Copy())
		return outgoing, conn, nil
	}
}

// AuthorityFunctionIDExtractor reads the :authority pseudo header and treats its host part as the function id.
func AuthorityFunctionIDExtractor() FunctionIDExtractor {
	return func(ctx context.Context, md metadata.MD) (string, error) {
		authorityValues := md[":authority"]
		if len(authorityValues) == 0 {
			authorityValues = md["authority"]
		}
		if len(authorityValues) == 0 {
			return "", status.Error(codes.InvalidArgument, "missing :authority header for function routing")
		}
		host := authorityValues[0]
		if idx := strings.Index(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		if host == "" {
			return "", status.Error(codes.InvalidArgument, "empty function id extracted from :authority")
		}
		return host, nil
	}
}
