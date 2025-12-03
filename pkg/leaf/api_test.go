//go:build integration

package leafv2

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/controlplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	leafproxy "github.com/3s-rg-codes/HyperFaaS/pkg/leaf/proxy"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	functionpb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const TEST_CONCURRENCY = 10000
const TEST_TIMEOUT = 10 * time.Second

func TestProxyCallViaAuthority(t *testing.T) {
	server, err := setup(t)
	if err != nil {
		t.Fatalf("failed to setup test server: %v", err)
	}

	id, err := server.metadataClient.PutFunction(context.Background(), &common.CreateFunctionRequest{
		Image: &common.Image{
			Tag: "test-image",
		},
		Config: &common.Config{
			Memory:         100 * 1024 * 1024,
			MaxConcurrency: 100000,
			Timeout:        10,
		},
	})
	if err != nil {
		t.Fatalf("failed to register function: %v", err)
	}

	time.Sleep(2 * time.Second)

	addr := "127.0.0.1:56789"
	reqCtx, reqCancel := context.WithTimeout(t.Context(), TEST_TIMEOUT)
	t.Cleanup(reqCancel)
	funcCtx, cancel := context.WithCancel(reqCtx)
	t.Cleanup(cancel)

	go func() {
		if err := (mockedRunningInstance{}).Run(funcCtx, addr); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("mock function server exited: %v", err)
		}
	}()

	proxyLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for proxy: %v", err)
	}
	t.Cleanup(func() { _ = proxyLis.Close() })

	proxyServer := grpc.NewServer(
		leafproxy.RoutingProxyOpt(
			server.ProxyBackendResolver(),
			leafproxy.AuthorityFunctionIDExtractor(),
		),
	)
	t.Cleanup(proxyServer.GracefulStop)
	go func() {
		if err := proxyServer.Serve(proxyLis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("proxy server stopped: %v", err)
		}
	}()

	conn, err := grpc.NewClient(
		proxyLis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(id),
	)
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := functionpb.NewFunctionServiceClient(conn)
	resp, err := client.Call(reqCtx, &common.CallRequest{
		FunctionId: id,
		Data:       []byte("via-proxy"),
	})
	if err != nil {
		t.Fatalf("proxy Call failed: %v", err)
	}
	if resp == nil || string(resp.Data) != "test-data" {
		t.Fatalf("unexpected proxy response: %v", resp)
	}
}

func setup(t *testing.T) (*Server, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := config.Config{
		WorkerAddresses: []string{"testesttstes"},
	}
	cfg.ApplyDefaults()
	server, err := newTestServer(ctx,
		cfg,
		metadata.NewMockClient(),
		slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err != nil {
		return nil, err
	}
	t.Cleanup(server.cancel)
	return server, nil
}

// NewServer initialises a leaf server with the provided configuration and logger,
// but uses mocked metadata client and mocked worker clients.
func newTestServer(ctx context.Context, cfg config.Config, metadataClient metadata.Client, logger *slog.Logger) (*Server, error) {
	cfg.ApplyDefaults()

	if len(cfg.WorkerAddresses) == 0 {
		return nil, errors.New("at least one worker address is required")
	}
	if logger == nil {
		panic("logger must not be nil")
	}
	if metadataClient == nil {
		panic("metadata client must not be nil")
	}
	metricChan := make(chan metrics.MetricEvent, METRIC_CHANNEL_BUFFER)
	// channel to notify of changes in the instance count of a function.
	// the direction of communication here is ControlPlane -> DataPlane.
	// Used for example when a new instance is started and we need to update the data plane,
	// so we can route calls to the new instance.
	instanceChangesChan := make(chan metrics.InstanceChange, INSTANCE_CHANGES_CHANNEL_BUFFER)

	// where the State stream reads zero scale events from.
	functionScaleEvents := make(chan metrics.ZeroScaleEvent, STATE_STREAM_BUFFER)
	serverCtx, cancel := context.WithCancel(ctx)

	// create worker clients
	workers := make([]*dataplane.WorkerClient, 0, len(cfg.WorkerAddresses))
	for idx, addr := range cfg.WorkerAddresses {
		h, err := dataplane.NewMockWorkerClient(serverCtx, idx, addr, cfg, logger.With("worker", addr))
		if err != nil {
			panic(err)
		}
		workers = append(workers, h)
	}

	cr := metrics.NewConcurrencyReporter(logger, metricChan, 1*time.Second)
	go cr.Run(serverCtx)

	dp := dataplane.NewDataPlane(logger, metadataClient, instanceChangesChan, cr)
	go dp.Run(serverCtx)

	cp := controlplane.NewControlPlane(serverCtx, cfg, logger, instanceChangesChan, workers, functionScaleEvents, cr)
	go cp.Run(serverCtx)

	s := &Server{
		cfg:                 cfg,
		logger:              logger,
		ctx:                 serverCtx,
		cancel:              cancel,
		nodeID:              uuid.NewString(),
		metadataClient:      metadataClient,
		dataPlane:           dp,
		controlPlane:        cp,
		concurrencyReporter: cr,
		functionScaleEvents: functionScaleEvents,
	}

	s.workers = workers

	if err := s.bootstrapMetadata(); err != nil {
		cancel()
		return nil, err
	}

	for _, w := range s.workers {
		w.StartStatusStream(s.nodeID, cfg.StatusBackoff, s.handleWorkerStatus)
	}

	return s, nil
}

var _ functionpb.FunctionServiceServer = mockedRunningInstance{}

type mockedRunningInstance struct {
	functionpb.UnimplementedFunctionServiceServer
}

func (m mockedRunningInstance) Call(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	return &common.CallResponse{
		Data: []byte("test-data"),
	}, nil
}

func (m mockedRunningInstance) Run(ctx context.Context, addr string) error {
	s := grpc.NewServer()
	functionpb.RegisterFunctionServiceServer(s, m)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		if err := s.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		s.GracefulStop()
		if err := <-errCh; err != nil && err != grpc.ErrServerStopped {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
