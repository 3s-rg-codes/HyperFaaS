package grpcproxy

import (
	"context"
	"sync"

	sideroproxy "github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DialBackend opens a gRPC connection to a leaf transparent proxy endpoint.
func DialBackend(_ context.Context, address string) (*grpc.ClientConn, error) {
	return grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodecV2(sideroproxy.Codec())),
	)
}

// ConnPool reuses transparent proxy connections keyed by address.
type ConnPool struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

func NewConnPool() *ConnPool {
	return &ConnPool{conns: make(map[string]*grpc.ClientConn)}
}

func (p *ConnPool) Get(_ context.Context, address string) (*grpc.ClientConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn, ok := p.conns[address]; ok {
		return conn, nil
	}
	conn, err := DialBackend(context.Background(), address)
	if err != nil {
		return nil, err
	}
	p.conns[address] = conn
	return conn, nil
}

func (p *ConnPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr, conn := range p.conns {
		_ = conn.Close()
		delete(p.conns, addr)
	}
}
