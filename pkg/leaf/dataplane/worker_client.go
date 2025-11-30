package dataplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
)

// WorkerClient provides a common interface for all worker clients.
/* type WorkerClient interface {
	Start(ctx context.Context, req *workerpb.StartRequest) (*workerpb.StartResponse, error)
	Stop(ctx context.Context, req *workerpb.StopRequest) (*workerpb.StopResponse, error)
	Address() string
	StartStatusStream(nodeID string, backoff time.Duration, cb func(int, *WorkerStatusEvent))
} */

// WorkerClient is a wrapper around the workerpb.WorkerClient that adds context cancellation and timeout handling.
type WorkerClient struct {
	// this workers id
	index   int
	address string

	ctx    context.Context
	cancel context.CancelFunc

	conn   *grpc.ClientConn
	client workerpb.WorkerClient

	logger *slog.Logger

	callTimeout  time.Duration
	startTimeout time.Duration
	stopTimeout  time.Duration

	// to make sure that we only start one status stream per worker
	statusOnce sync.Once
}

func NewWorkerClient(ctx context.Context, idx int, addr string, cfg config.Config, logger *slog.Logger) (*WorkerClient, error) {
	subCtx, cancel := context.WithCancel(ctx)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		return nil, err
	}

	return &WorkerClient{
		index:        idx,
		address:      addr,
		ctx:          subCtx,
		cancel:       cancel,
		conn:         conn,
		client:       workerpb.NewWorkerClient(conn),
		logger:       logger,
		callTimeout:  cfg.CallTimeout,
		startTimeout: cfg.StartTimeout,
		stopTimeout:  cfg.StopTimeout,
	}, nil
}

// for testing
func NewMockWorkerClient(ctx context.Context, idx int, addr string, cfg config.Config, logger *slog.Logger) (*WorkerClient, error) {
	subCtx, cancel := context.WithCancel(ctx)
	return &WorkerClient{
		index:        idx,
		address:      addr,
		ctx:          subCtx,
		cancel:       cancel,
		conn:         nil,
		client:       newMockWorkerClient(),
		logger:       logger,
		callTimeout:  cfg.CallTimeout,
		startTimeout: cfg.StartTimeout,
		stopTimeout:  cfg.StopTimeout,
	}, nil
}

func (w *WorkerClient) Start(ctx context.Context, req *workerpb.StartRequest) (*workerpb.StartResponse, error) {
	startCtx, cancel := context.WithTimeout(ctx, w.startTimeout)
	defer cancel()
	return w.client.Start(startCtx, req)
}

func (w *WorkerClient) Stop(ctx context.Context, req *workerpb.StopRequest) (*workerpb.StopResponse, error) {
	stopCtx, cancel := context.WithTimeout(ctx, w.stopTimeout)
	defer cancel()
	return w.client.Stop(stopCtx, req)
}

func (w *WorkerClient) Address() string {
	return w.address
}

// startStatusStream starts the status stream in a goroutine if it hasn't been started yet.
func (w *WorkerClient) StartStatusStream(nodeID string, backoff time.Duration, cb func(int, *WorkerStatusEvent)) {
	w.statusOnce.Do(func() {
		go w.runStatusStream(nodeID, backoff, cb)
	})
}

// runStatusStream reads from the workers status stream in a loop, and calls the callback function with the status events.
func (w *WorkerClient) runStatusStream(nodeID string, backoff time.Duration, cb func(int, *WorkerStatusEvent)) {
	if backoff <= 0 {
		backoff = time.Second
	}
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		streamCtx, cancel := context.WithCancel(w.ctx)
		stream, err := w.client.Status(streamCtx, &workerpb.StatusRequest{NodeId: nodeID})
		if err != nil {
			cancel()
			if status.Code(err) != codes.Canceled {
				w.logger.Warn("worker status stream dial failed", "error", err)
			}
			select {
			case <-time.After(backoff):
			case <-w.ctx.Done():
				return
			}
			continue
		}

		for {
			update, recvErr := stream.Recv()
			if recvErr != nil {
				cancel()
				if w.ctx.Err() != nil {
					return
				}
				w.logger.Debug("worker status stream ended", "error", recvErr)
				break
			}
			cb(w.index, TranslateWorkerStatus(update))
		}

		cancel()

		select {
		case <-time.After(backoff):
		case <-w.ctx.Done():
			return
		}
	}
}

type WorkerStatusEvent struct {
	FunctionId string
	InstanceId string
	Event      workerpb.Event
	Status     workerpb.Status
}

// TranslateWorkerStatus turns a workerpb.StatusUpdate into workerStatusEvent
func TranslateWorkerStatus(update *workerpb.StatusUpdate) *WorkerStatusEvent {
	if update == nil {
		return nil
	}
	return &WorkerStatusEvent{
		FunctionId: update.FunctionId,
		InstanceId: update.InstanceId,
		Event:      update.Event,
		Status:     update.Status,
	}
}

type mockWorkerClient struct{}

func newMockWorkerClient() workerpb.WorkerClient {
	return mockWorkerClient{}
}

// Metrics implements worker.WorkerClient.
func (m mockWorkerClient) Metrics(ctx context.Context, in *workerpb.MetricsRequest, opts ...grpc.CallOption) (*workerpb.MetricsUpdate, error) {
	return &workerpb.MetricsUpdate{
		UsedRamPercent:    0,
		CpuPercentPercpus: []float64{0},
	}, nil
}

// SignalReady implements worker.WorkerClient.
func (m mockWorkerClient) SignalReady(ctx context.Context, in *workerpb.SignalReadyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// Start implements worker.WorkerClient.
func (m mockWorkerClient) Start(ctx context.Context, in *workerpb.StartRequest, opts ...grpc.CallOption) (*workerpb.StartResponse, error) {

	if in.FunctionId == "fail" {
		return nil, errors.New("expected error")
	}

	return &workerpb.StartResponse{
		InstanceId:         "instance-id",
		InstanceInternalIp: "127.0.0.1:56789",
		InstanceExternalIp: "127.0.0.1:56789",
		InstanceName:       "mock-instance",
	}, nil
}

// Status implements worker.WorkerClient.
func (m mockWorkerClient) Status(ctx context.Context, in *workerpb.StatusRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[workerpb.StatusUpdate], error) {
	return &mockStatusStream{ctx: ctx}, nil
}

// Stop implements worker.WorkerClient.
func (m mockWorkerClient) Stop(ctx context.Context, in *workerpb.StopRequest, opts ...grpc.CallOption) (*workerpb.StopResponse, error) {
	return &workerpb.StopResponse{
		InstanceId: "instance-id",
	}, nil
}

var _ workerpb.WorkerClient = mockWorkerClient{}

type mockStatusStream struct {
	ctx context.Context
}

func (m *mockStatusStream) Header() (metadata.MD, error) {
	return nil, nil
}

func (m *mockStatusStream) Trailer() metadata.MD {
	return nil
}

func (m *mockStatusStream) CloseSend() error {
	return nil
}

func (m *mockStatusStream) Context() context.Context {
	return m.ctx
}

func (m *mockStatusStream) SendMsg(interface{}) error {
	return nil
}

func (m *mockStatusStream) RecvMsg(interface{}) error {
	return io.EOF
}

func (m *mockStatusStream) Recv() (*workerpb.StatusUpdate, error) {
	return nil, io.EOF
}
