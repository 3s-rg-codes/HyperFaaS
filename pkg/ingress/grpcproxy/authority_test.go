package grpcproxy_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	"hyperfaas-ideal-arch/pkg/ingress/grpcproxy"
)

func TestFunctionIDFromMetadataPrefersHeader(t *testing.T) {
	md := metadata.Pairs(
		grpcproxy.MetadataFunctionID, "42",
		":authority", "99:50052",
	)
	id, err := grpcproxy.FunctionIDFromMetadata(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("got %d", id)
	}
}

func TestFunctionIDFromMetadataAuthority(t *testing.T) {
	md := metadata.Pairs(":authority", "42:50052")
	id, err := grpcproxy.FunctionIDFromMetadata(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("got %d", id)
	}
}

func TestFunctionIDFromMetadataInvalidHeader(t *testing.T) {
	md := metadata.Pairs(grpcproxy.MetadataFunctionID, "not-a-number")
	_, err := grpcproxy.FunctionIDFromMetadata(context.Background(), md)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() == "" {
		t.Fatal("expected message")
	}
}

func TestFunctionIDFromMetadataInvalidAuthority(t *testing.T) {
	md := metadata.Pairs(":authority", "bad-id")
	_, err := grpcproxy.FunctionIDFromMetadata(context.Background(), md)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFunctionIDFromMetadataMissingBoth(t *testing.T) {
	_, err := grpcproxy.FunctionIDFromMetadata(context.Background(), metadata.MD{})
	if err == nil {
		t.Fatal("expected error")
	}
}
