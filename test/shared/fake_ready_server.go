package shared

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

// FakeReadyServer implements the worker sandbox service to capture guest SignalReady callbacks.
type FakeReadyServer struct {
	workerpb.UnimplementedSandboxServiceServer
	server *grpc.Server

	mu       sync.Mutex
	received map[uint64]bool
	readyCh  chan uint64
}

func NewFakeReadyServer() *FakeReadyServer {
	return &FakeReadyServer{
		received: make(map[uint64]bool),
		readyCh:  make(chan uint64, 1000),
	}
}

func (s *FakeReadyServer) SignalReady(ctx context.Context, req *workerpb.SignalReadyRequest) (*workerpb.SignalReadyResponse, error) {
	s.mu.Lock()
	s.received[req.GetInstanceId()] = true
	s.mu.Unlock()

	select {
	case s.readyCh <- req.GetInstanceId():
	default:
	}
	return &workerpb.SignalReadyResponse{}, nil
}

// ReadyChannel returns a receive-only channel that emits guest instance IDs upon readiness signal.
func (s *FakeReadyServer) ReadyChannel() <-chan uint64 {
	return s.readyCh
}

// WasReceived returns true if the specific instance ID sent a SignalReady callback.
func (s *FakeReadyServer) WasReceived(instanceID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.received[instanceID]
}

// StartFakeReadyServer starts the gRPC mock server on a free port on all interfaces (0.0.0.0).
func StartFakeReadyServer(t testing.TB) (string, *FakeReadyServer, func()) {
	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("failed to listen for fake ready server: %v", err)
	}

	server := grpc.NewServer()
	srv := NewFakeReadyServer()
	srv.server = server
	workerpb.RegisterSandboxServiceServer(server, srv)

	go func() {
		_ = server.Serve(lis)
	}()

	cleanup := func() {
		server.Stop()
		_ = lis.Close()
	}

	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		cleanup()
		t.Fatalf("failed to split fake ready server address: %v", err)
	}

	return net.JoinHostPort("0.0.0.0", port), srv, cleanup
}
