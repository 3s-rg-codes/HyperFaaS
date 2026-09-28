package grpcproxy

import (
	"context"

	sideroproxy "github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DialBackend opens a gRPC connection to a function instance using the proxy codec.
// Required for transparent proxy forwarding; ordinary proto clients must not use this.
func DialBackend(ctx context.Context, address string) (*grpc.ClientConn, error) {
	_ = ctx
	return grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodecV2(sideroproxy.Codec())),
	)
}
