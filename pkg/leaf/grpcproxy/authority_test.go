package grpcproxy

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestFunctionIDFromAuthorityHostPort(t *testing.T) {
	md := metadata.Pairs(":authority", "42:50052")
	id, err := FunctionIDFromAuthority(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("got %d", id)
	}
}
