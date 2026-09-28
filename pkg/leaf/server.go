package leaf

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/utils"
	leafstate "hyperfaas-ideal-arch/pkg/leaf/state"
)

// LeafServer hosts the control API and the HTTP invocation server.
type LeafServer struct {
	cfg      LeafConfig
	logger   *slog.Logger
	control  ControlService
	state    StateReporter
	registry FunctionRegistry
}

type leafServerOption func(*LeafServer)

func WithControlService(svc ControlService) leafServerOption {
	return func(s *LeafServer) { s.control = svc }
}

func WithStateReporter(reporter StateReporter) leafServerOption {
	return func(s *LeafServer) { s.state = reporter }
}

// WithFunctionRegistry supplies the leaf request registry.
func WithFunctionRegistry(registry FunctionRegistry) leafServerOption {
	return func(s *LeafServer) { s.registry = registry }
}

// NewServer builds a leaf server from configuration.
func NewServer(cfg LeafConfig, logger *slog.Logger, opts ...leafServerOption) (*LeafServer, error) {
	if logger == nil {
		return nil, fmt.Errorf("leaf: logger is required")
	}
	s := &LeafServer{cfg: cfg, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Run starts pprof (if configured) and blocks serving gRPC (plus the optional
// streaming HTTP invocation listener) until ctx is cancelled.
func (s *LeafServer) Run(ctx context.Context) error {
	stopPprof := utils.StartPprof(ctx, s.logger, s.cfg.Server.PprofAddress)
	defer stopPprof()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	listeners := 1

	controlHandler := &grpcLeafControlService{service: s.control, state: s.state}

	if s.cfg.HTTPInvocationAddress != "" && s.registry != nil {
		listeners++
		handler := newHTTPInvocationHandler(s.logger, s.registry, s.cfg.Dataplane)
		go func() {
			errCh <- RunHTTPInvocationServer(ctx, s.logger, s.cfg.HTTPInvocationAddress, handler)
		}()
	}

	go func() {
		errCh <- utils.RunGRPCServer(ctx, utils.GRPCServerOptions{
			Logger:      s.logger,
			ListenAddr:  s.cfg.Server.ListenAddress,
			ServiceName: "leaf",
			Register: func(grpcServer *grpc.Server) {
				RegisterLeafControlServiceServer(grpcServer, controlHandler)
			},
		})
	}()

	select {
	case <-ctx.Done():
		for range listeners {
			<-errCh
		}
		return ctx.Err()
	case err := <-errCh:
		cancel()
		for i := 1; i < listeners; i++ {
			<-errCh
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
		return ctx.Err()
	}
}

type grpcLeafControlService struct {
	UnimplementedLeafControlServiceServer
	service ControlService
	state   StateReporter
}

func (g *grpcLeafControlService) ApplyFunction(ctx context.Context, req *ApplyFunctionRequest) (*ApplyFunctionResponse, error) {
	if g.service == nil {
		return nil, status.Error(codes.Unimplemented, "leaf control handler not configured")
	}
	if err := g.service.ApplyFunction(ctx, req.GetFunction()); err != nil {
		return nil, err
	}
	return &ApplyFunctionResponse{}, nil
}

func (g *grpcLeafControlService) DeleteFunction(ctx context.Context, req *DeleteLeafFunctionRequest) (*DeleteLeafFunctionResponse, error) {
	if g.service == nil {
		return nil, status.Error(codes.Unimplemented, "leaf control handler not configured")
	}
	if err := g.service.DeleteFunction(ctx, req.GetFunctionId()); err != nil {
		return nil, err
	}
	return &DeleteLeafFunctionResponse{}, nil
}

func (g *grpcLeafControlService) EnsureCapacity(ctx context.Context, req *EnsureCapacityRequest) (*EnsureCapacityResponse, error) {
	if g.service == nil {
		return nil, status.Error(codes.Unimplemented, "leaf control handler not configured")
	}
	if err := g.service.EnsureCapacity(ctx, req.GetDemand()); err != nil {
		return nil, err
	}
	return &EnsureCapacityResponse{}, nil
}

// CurrentState serves the diagnostic full leaf snapshot. It is only for DST
// tooling and tests and is not used on the routing path.
func (g *grpcLeafControlService) CurrentState(ctx context.Context, _ *CurrentLeafStateRequest) (*core.LeafState, error) {
	if g.state == nil {
		return nil, status.Error(codes.Unimplemented, "leaf state reporter not configured")
	}
	return g.state.CurrentState(ctx)
}

func (g *grpcLeafControlService) WatchRoutingState(req *WatchRoutingStateRequest, stream grpc.ServerStreamingServer[RoutingStateFrame]) error {
	if g.state == nil {
		return status.Error(codes.Unimplemented, "leaf state reporter not configured")
	}

	projection := leafstate.RoutingProjection{
		LeafLoad:          req.GetProjection().GetLeafLoad(),
		AggregateInFlight: req.GetProjection().GetAggregateInFlight(),
		FunctionCapacity:  req.GetProjection().GetFunctionCapacity(),
	}
	frames, errs := g.state.WatchRoutingState(stream.Context(), req.GetConfigVersion(), projection)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-errs:
			if err != nil {
				return err
			}
		case frame, ok := <-frames:
			if !ok {
				return nil
			}
			if err := stream.Send(routingFrameToProto(frame)); err != nil {
				return err
			}
		}
	}
}

// routingFrameToProto translates the state package's frame into the wire
// message. Optional scalar fields are set only when the projection produced
// them, which is how the wire preserves presence.
func routingFrameToProto(frame *leafstate.RoutingFrame) *RoutingStateFrame {
	if frame == nil {
		return &RoutingStateFrame{}
	}
	out := &RoutingStateFrame{
		ConfigVersion:  frame.ConfigVersion,
		LeafId:         frame.LeafID,
		Revision:       frame.Revision,
		FullSnapshot:   frame.FullSnapshot,
		HealthyWorkers: frame.HealthyWorkers,
		Capacities:     frame.Capacities,
	}
	if frame.HasLeafLoad {
		out.LeafLoad = &frame.LeafLoad
	}
	if frame.HasAggregateInFlight {
		out.AggregateInFlight = &frame.AggregateInFlight
	}
	return out
}
