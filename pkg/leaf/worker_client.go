package leafv2

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
)

// workerClient is a wrapper around the workerpb.WorkerClient that adds context cancellation and timeout handling.
type workerClient struct {
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

func newWorkerClient(ctx context.Context, idx int, addr string, cfg Config, logger *slog.Logger) (*workerClient, error) {
	subCtx, cancel := context.WithCancel(ctx)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		return nil, err
	}

	return &workerClient{
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

func (w *workerClient) close() error {
	w.cancel()
	if w.conn != nil {
		return w.conn.Close()
	}
	return nil
}

func (w *workerClient) Start(ctx context.Context, req *workerpb.StartRequest) (*workerpb.StartResponse, error) {
	startCtx, cancel := context.WithTimeout(ctx, w.startTimeout)
	defer cancel()
	return w.client.Start(startCtx, req)
}

func (w *workerClient) Stop(ctx context.Context, req *workerpb.StopRequest) (*workerpb.StopResponse, error) {
	stopCtx, cancel := context.WithTimeout(ctx, w.stopTimeout)
	defer cancel()
	return w.client.Stop(stopCtx, req)
}

func (w *workerClient) Address() string {
	return w.address
}

// startStatusStream starts the status stream in a goroutine if it hasn't been started yet.
func (w *workerClient) startStatusStream(nodeID string, backoff time.Duration, cb func(int, *workerStatusEvent)) {
	w.statusOnce.Do(func() {
		go w.runStatusStream(nodeID, backoff, cb)
	})
}

// runStatusStream reads from the workers status stream in a loop, and calls the callback function with the status events.
func (w *workerClient) runStatusStream(nodeID string, backoff time.Duration, cb func(int, *workerStatusEvent)) {
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
			cb(w.index, translateWorkerStatus(update))
		}

		cancel()

		select {
		case <-time.After(backoff):
		case <-w.ctx.Done():
			return
		}
	}
}

type workerStatusEvent struct {
	functionID string
	instanceID string
	event      workerpb.Event
	status     workerpb.Status
}

// translateWorkerStatus turns a workerpb.StatusUpdate into workerStatusEvent
func translateWorkerStatus(update *workerpb.StatusUpdate) *workerStatusEvent {
	if update == nil {
		return nil
	}
	return &workerStatusEvent{
		functionID: update.FunctionId,
		instanceID: update.InstanceId,
		event:      update.Event,
		status:     update.Status,
	}
}
