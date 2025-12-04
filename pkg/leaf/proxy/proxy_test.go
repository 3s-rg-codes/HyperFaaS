//go:build unit

package proxy

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type stubConn struct{}

func (stubConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	return nil
}

func (stubConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestRoutingDirectorWithResolverFunc(t *testing.T) {
	const functionID = "test"
	var (
		resolvedWith   string
		receivedMethod string
	)

	resolver := BackendResolverFunc(func(ctx context.Context, fnID string, fullMethodName string) (grpc.ClientConnInterface, error) {
		resolvedWith = fnID
		receivedMethod = fullMethodName
		return stubConn{}, nil
	})

	director := RoutingDirector(resolver, AuthorityFunctionIDExtractor())

	incomingMD := metadata.New(map[string]string{":authority": functionID})
	ctx := metadata.NewIncomingContext(context.Background(), incomingMD)

	outCtx, conn, err := director(ctx, "/pkg.Service/Method")
	if err != nil {
		t.Fatalf("director returned error: %v", err)
	}
	if conn == nil {
		t.Fatalf("director returned nil conn")
	}

	if resolvedWith != functionID {
		t.Fatalf("resolver invoked with %q, want %q", resolvedWith, functionID)
	}
	if receivedMethod != "/pkg.Service/Method" {
		t.Fatalf("resolver method %q, want /pkg.Service/Method", receivedMethod)
	}

	md, ok := metadata.FromOutgoingContext(outCtx)
	if !ok {
		t.Fatalf("no outgoing metadata found")
	}
	if got := md[":authority"]; len(got) != 1 || got[0] != functionID {
		t.Fatalf("outgoing metadata lost authority header: %v", got)
	}
}

func TestWrapClientConnReleasesOnce(t *testing.T) {
	calls := 0
	wrapped := WrapClientConn(stubConn{}, func(error) {
		calls++
	})

	releasable, ok := wrapped.(releaseAwareConn)
	if !ok {
		t.Fatalf("wrapped connection does not expose release")
	}

	releasable.Release(nil)
	releasable.Release(nil)
	releasable.Release(fmt.Errorf("second"))

	if calls != 1 {
		t.Fatalf("release invoked %d times, want 1", calls)
	}
}
