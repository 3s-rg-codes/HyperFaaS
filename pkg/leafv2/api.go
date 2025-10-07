package leafv2

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
)

// Server implements the leafpb.LeafServer gRPC.
type Server struct {
	leafpb.UnimplementedLeafServer

	cfg    Config
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	workers []*workerClient
	// the id of this node. used to subscribe to status stream of workers.
	nodeID string

	mu sync.RWMutex
	// functionId -> controller to scale and route calls.
	functions map[string]*functionController
}

// NewServer wires a LeafV2 server with the provided configuration and logger.
func NewServer(ctx context.Context, cfg Config, logger *slog.Logger) (*Server, error) {
	cfg.applyDefaults()

	if len(cfg.WorkerAddresses) == 0 {
		return nil, fmt.Errorf("at least one worker address is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}

	serverCtx, cancel := context.WithCancel(ctx)
	s := &Server{
		cfg:       cfg,
		logger:    logger,
		ctx:       serverCtx,
		cancel:    cancel,
		nodeID:    uuid.NewString(),
		functions: make(map[string]*functionController),
	}

	workers := make([]*workerClient, 0, len(cfg.WorkerAddresses))
	for idx, addr := range cfg.WorkerAddresses {
		h, err := newWorkerClient(serverCtx, idx, addr, cfg, logger.With("worker", addr))
		if err != nil {
			cancel()
			s.closeWorkers(workers)
			return nil, fmt.Errorf("dial worker %s: %w", addr, err)
		}
		workers = append(workers, h)
	}
	s.workers = workers

	for _, w := range s.workers {
		w.startStatusStream(s.nodeID, cfg.StatusBackoff, s.handleWorkerStatus)
	}

	return s, nil
}

// Close releases all resources
func (s *Server) Close() error {
	s.cancel()
	s.closeWorkers(s.workers)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ctrl := range s.functions {
		ctrl.stop()
		delete(s.functions, id)
	}
	return nil
}

func (s *Server) closeWorkers(workers []*workerClient) {
	for _, w := range workers {
		if w != nil {
			if err := w.close(); err != nil {
				s.logger.Warn("closing worker", "worker", w.address, "error", err)
			}
		}
	}
}

// RegisterFunction initialises scaling state for a function.
func (s *Server) RegisterFunction(ctx context.Context, req *leafpb.RegisterFunctionRequest) (*common.CreateFunctionResponse, error) {
	if req == nil || req.Config == nil || req.FunctionId == "" {
		return nil, status.Error(codes.InvalidArgument, "function config and ID are required")
	}

	cfg := req.Config.GetConfig()
	if cfg == nil {
		return nil, status.Error(codes.InvalidArgument, "function config is required")
	}

	functionID := req.FunctionId

	s.mu.Lock()
	ctrl, exists := s.functions[functionID]
	if exists {
		ctrl.updateConfig(cfg)
		s.mu.Unlock()
		return &common.CreateFunctionResponse{FunctionId: functionID}, nil
	}

	ctrl = newFunctionController(s.ctx, functionID, cfg, s.workers, s.cfg, s.logger.With("function", functionID))
	s.functions[functionID] = ctrl
	s.mu.Unlock()

	return &common.CreateFunctionResponse{FunctionId: functionID}, nil
}

// ScheduleCall forwards a call request to one of the managed workers, scaling pods as needed.
func (s *Server) ScheduleCall(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	if req == nil || req.FunctionId == "" {
		return nil, status.Error(codes.InvalidArgument, "function_id is required")
	}

	ctrl := s.getFunction(req.FunctionId)
	if ctrl == nil {
		return nil, status.Errorf(codes.NotFound, "function %s is not registered", req.FunctionId)
	}

	resp, err := ctrl.routeCall(ctx, req)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			return nil, st.Err()
		}
		return nil, status.Errorf(codes.Unavailable, "call failed: %v", err)
	}
	return resp, nil
}

func (s *Server) getFunction(functionID string) *functionController {
	s.mu.RLock()
	ctrl := s.functions[functionID]
	s.mu.RUnlock()
	return ctrl
}

func (s *Server) handleWorkerStatus(workerIdx int, update *workerStatusEvent) {
	if update == nil || update.functionID == "" {
		return
	}
	ctrl := s.getFunction(update.functionID)
	if ctrl == nil {
		return
	}
	ctrl.handleWorkerEvent(workerIdx, update)
}
