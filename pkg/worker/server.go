package worker

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/utils"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
)

// WorkerServer hosts the worker's gRPC services.
type WorkerServer struct {
	cfg     WorkerConfig
	logger  *slog.Logger
	sandbox SandboxService
	health  HealthService
}

type workerServerOption func(*WorkerServer)

func WithSandboxService(svc SandboxService) workerServerOption {
	return func(s *WorkerServer) { s.sandbox = svc }
}

func WithHealthService(svc HealthService) workerServerOption {
	return func(s *WorkerServer) { s.health = svc }
}

// NewServer builds a worker server from configuration.
func NewServer(cfg WorkerConfig, logger *slog.Logger, opts ...workerServerOption) (*WorkerServer, error) {
	if logger == nil {
		return nil, fmt.Errorf("worker: logger is required")
	}
	s := &WorkerServer{cfg: cfg, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	if s.sandbox == nil {
		sandbox, err := NewSandbox(cfg, logger)
		if err != nil {
			return nil, err
		}
		s.sandbox = sandbox
	}
	if s.health == nil {
		s.health = NewProcHealthService(cfg, s.sandbox)
	}
	return s, nil
}

// Run starts pprof (if configured) and blocks serving gRPC until ctx is cancelled.
func (s *WorkerServer) Run(ctx context.Context) error {
	stopPprof := utils.StartPprof(ctx, s.logger, s.cfg.Server.PprofAddress)
	defer stopPprof()

	handler := &grpcSandboxService{
		sandbox: s.sandbox,
		health:  s.health,
		logger:  s.logger,
	}

	return utils.RunGRPCServer(ctx, utils.GRPCServerOptions{
		Logger:      s.logger,
		ListenAddr:  s.cfg.Server.ListenAddress,
		ServiceName: "worker",
		Register: func(grpcServer *grpc.Server) {
			workerpb.RegisterSandboxServiceServer(grpcServer, handler)
		},
	})
}

type grpcSandboxService struct {
	workerpb.UnimplementedSandboxServiceServer
	sandbox SandboxService
	health  HealthService
	logger  *slog.Logger
}

func (g *grpcSandboxService) PrepareImage(ctx context.Context, req *workerpb.PrepareImageRequest) (*core.PreparedArtifact, error) {
	if g.sandbox == nil {
		return nil, status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	return g.sandbox.PrepareImage(ctx, req.GetFunction())
}

func (g *grpcSandboxService) CreateSandbox(ctx context.Context, req *workerpb.CreateSandboxRequest) (*core.InstanceState, error) {
	if g.sandbox == nil {
		return nil, status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	startReq := req.GetRequest()
	functionID := uint64(0)
	if startReq.GetFunction() != nil {
		functionID = startReq.GetFunction().GetFunctionId()
	}
	g.logger.Info("start_sandbox_rpc_received",
		"function_id", functionID,
		"instance_id", startReq.GetInstanceId(),
		"worker_id", startReq.GetWorkerId(),
	)
	return g.sandbox.CreateSandbox(ctx, req.GetRequest())
}

func (g *grpcSandboxService) StopSandbox(ctx context.Context, req *workerpb.StopSandboxRequest) (*workerpb.StopSandboxResponse, error) {
	if g.sandbox == nil {
		return nil, status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	if err := g.sandbox.StopSandbox(ctx, req.GetInstanceId()); err != nil {
		return nil, err
	}
	return &workerpb.StopSandboxResponse{}, nil
}

// ListSandboxes returns every sandbox with per-sandbox resource usage. It is
// very inefficient: one sequential runtime Stats call per ready sandbox. It is
// intended only for tests and debugging, never for a hot path. Hot paths use
// CurrentState (fast identity snapshot) and WatchSandboxEvents.
func (g *grpcSandboxService) ListSandboxes(ctx context.Context, _ *workerpb.ListSandboxesRequest) (*workerpb.ListSandboxesResponse, error) {
	if g.sandbox == nil {
		return nil, status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	instances, err := g.sandbox.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	return &workerpb.ListSandboxesResponse{Instances: instances}, nil
}

// WatchSandboxEvents streams best-effort sandbox removal deltas to the owning
// leaf. This is the low-latency eviction path; WatchState remains the periodic,
// revisioned relist that repairs any dropped event.
func (g *grpcSandboxService) WatchSandboxEvents(_ *workerpb.WatchSandboxEventsRequest, stream workerpb.SandboxService_WatchSandboxEventsServer) error {
	if g.sandbox == nil {
		return status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	subscriber, ok := g.sandbox.(interface {
		SubscribeSandboxRemovals(context.Context) <-chan SandboxRemoval
	})
	if !ok {
		return status.Error(codes.Unimplemented, "sandbox removal events not supported")
	}
	removals := subscriber.SubscribeSandboxRemovals(stream.Context())
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case removal, ok := <-removals:
			if !ok {
				return nil
			}
			if err := stream.Send(&core.SandboxRemoved{
				InstanceId:     removal.InstanceID,
				FunctionId:     removal.FunctionID,
				WorkerRevision: removal.Revision,
			}); err != nil {
				return err
			}
		}
	}
}

func (g *grpcSandboxService) SignalReady(ctx context.Context, req *workerpb.SignalReadyRequest) (*workerpb.SignalReadyResponse, error) {
	if g.sandbox == nil {
		return nil, status.Error(codes.Unimplemented, "worker sandbox service not configured")
	}
	if err := g.sandbox.SignalReady(ctx, req.GetInstanceId()); err != nil {
		return nil, err
	}
	return &workerpb.SignalReadyResponse{}, nil
}

// CurrentState serves the diagnostic full worker snapshot. It is ungated and
// not used on the placement path; placement uses WatchState with a projection.
func (g *grpcSandboxService) CurrentState(ctx context.Context, _ *workerpb.CurrentWorkerStateRequest) (*core.WorkerState, error) {
	if g.health == nil {
		return nil, status.Error(codes.Unimplemented, "worker health service not configured")
	}
	return g.health.CurrentState(ctx)
}

func (g *grpcSandboxService) SetLoadOverride(_ context.Context, req *workerpb.SetLoadOverrideRequest) (*workerpb.SetLoadOverrideResponse, error) {
	overrider, ok := g.health.(interface {
		SetLoadAverageNormOverride(value float64, clear bool)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "worker health service does not support load override")
	}
	overrider.SetLoadAverageNormOverride(req.GetLoadAverageNorm(), req.GetClear())
	return &workerpb.SetLoadOverrideResponse{}, nil
}

func (g *grpcSandboxService) WatchState(req *workerpb.WatchWorkerStateRequest, stream grpc.ServerStreamingServer[core.WorkerState]) error {
	if g.health == nil {
		return status.Error(codes.Unimplemented, "worker health service not configured")
	}

	// The projection is the only gate. The worker does not subscribe to
	// PlatformConfig and has no applied config version to compare against, so it
	// does not reject a request whose config_version is newer. This is the
	// research-platform assumption: configuration changes only while no
	// invocation load is in flight, and a projection mismatch at worst computes
	// a signal the leaf ignores until it reopens the stream.
	projection := StateProjection{
		LoadAverageNorm: req.GetProjection().GetLoadAverageNorm(),
		CachedImages:    req.GetProjection().GetCachedImages(),
	}

	updates, errs := g.health.WatchState(stream.Context(), projection)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return err
			}
		case state, ok := <-updates:
			if !ok {
				return nil
			}
			if err := stream.Send(state); err != nil {
				return err
			}
		}
	}
}
