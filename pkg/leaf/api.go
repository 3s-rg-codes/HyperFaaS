package leafv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
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
	functions      map[string]*functionController
	metadataClient metadataClient
}

type metadataClient interface {
	ListFunctions(ctx context.Context) (*metadata.ListResult, error)
	WatchFunctions(ctx context.Context, revision int64) (<-chan metadata.Event, <-chan error)
}

// NewServer initialises a leaf server with the provided configuration and logger.
func NewServer(ctx context.Context, cfg Config, metadataClient metadataClient, logger *slog.Logger) (*Server, error) {
	cfg.applyDefaults()

	if len(cfg.WorkerAddresses) == 0 {
		return nil, errors.New("at least one worker address is required")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}
	if metadataClient == nil {
		return nil, errors.New("metadata client must not be nil")
	}

	serverCtx, cancel := context.WithCancel(ctx)
	s := &Server{
		cfg:            cfg,
		logger:         logger,
		ctx:            serverCtx,
		cancel:         cancel,
		nodeID:         uuid.NewString(),
		functions:      make(map[string]*functionController),
		metadataClient: metadataClient,
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

	if err := s.bootstrapMetadata(); err != nil {
		cancel()
		s.closeWorkers(workers)
		return nil, err
	}

	for _, w := range s.workers {
		w.startStatusStream(s.nodeID, cfg.StatusBackoff, s.handleWorkerStatus)
	}

	return s, nil
}

// Close releases all resources
func (s *Server) Close() error {
	s.cancel()
	s.closeWorkers(s.workers)
	ids := make([]string, 0)
	s.mu.RLock()
	for id := range s.functions {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	for _, id := range ids {
		s.removeFunction(id)
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

func (s *Server) bootstrapMetadata() error {
	list, err := s.metadataClient.ListFunctions(s.ctx)
	if err != nil {
		return fmt.Errorf("list function metadata: %w", err)
	}
	count := 0
	if list != nil {
		for _, fn := range list.Functions {
			s.upsertFunction(fn)
			count++
		}
		go s.watchMetadata(list.Revision)
	} else {
		go s.watchMetadata(0)
	}
	if count > 0 {
		s.logger.Info("initialised function controllers from metadata", "count", count)
	}
	return nil
}

func (s *Server) watchMetadata(startRevision int64) {
	events, errs := s.metadataClient.WatchFunctions(s.ctx, startRevision)
	eventCh := events
	errCh := errs

	for {
		select {
		case <-s.ctx.Done():
			return
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				s.logger.Error("metadata watch error", "error", err)
			}
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			s.handleMetadataEvent(ev)
		}
	}
}

func (s *Server) handleMetadataEvent(ev metadata.Event) {
	switch ev.Type {
	case metadata.EventTypePut:
		s.upsertFunction(ev.Function)
	case metadata.EventTypeDelete:
		if ev.FunctionID == "" {
			s.logger.Warn("metadata delete without function id")
			return
		}
		s.removeFunction(ev.FunctionID)
	default:
		s.logger.Warn("unknown metadata event", "type", ev.Type)
	}
}

func (s *Server) upsertFunction(meta *metadata.FunctionMetadata) {
	if meta == nil {
		return
	}
	if meta.ID == "" {
		s.logger.Warn("metadata missing function id")
		return
	}
	if meta.Config == nil {
		s.logger.Warn("metadata missing config", "functionID", meta.ID)
		return
	}

	s.mu.Lock()
	ctrl, exists := s.functions[meta.ID]
	if exists {
		s.mu.Unlock()
		ctrl.updateConfig(meta.Config)
		return
	}

	scaleChan := make(chan bool)
	ctrl = newFunctionController(s.ctx, meta.ID, meta.Config, s.workers, s.cfg, s.logger.With("function", meta.ID), scaleChan)
	s.functions[meta.ID] = ctrl
	s.mu.Unlock()

	s.logger.Info("registered function controller", "functionID", meta.ID)
}

func (s *Server) removeFunction(functionID string) {
	s.mu.Lock()
	ctrl, exists := s.functions[functionID]
	if exists {
		delete(s.functions, functionID)
	}
	s.mu.Unlock()

	if !exists {
		return
	}

	ctrl.stop()
	func() {
		defer func() {
			_ = recover()
		}()
		close(ctrl.scaleChan)
	}()
	s.logger.Info("removed function controller", "functionID", functionID)
}

// ScheduleCall forwards a call request to one of the managed workers
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

// State streams changes in state of a function_id.
// A change in the number of running instances.
// IMPORTANT: this is meant for only ONE client to be listening to.
// IMPORTANT: this does not handle additional functions being registered after the stream is established.
// To get the latest state of all functions, the client must call this again.
func (s *Server) State(req *common.StateRequest, stream leafpb.Leaf_StateServer) error {
	ctx := stream.Context()

	s.mu.RLock()
	wg := sync.WaitGroup{}

	for _, ctrl := range s.functions {
		wg.Go(func() {
			for have := range ctrl.scaleChan {
				// exit if context is done
				if ctx.Err() != nil {
					return
				}
				err := stream.Send(&common.StateResponse{
					FunctionId: ctrl.functionID,
					Have:       have,
				})
				if err != nil {
					s.logger.Error("failed to send state response", "error", err)
				}
			}
		})
	}
	s.mu.RUnlock()

	wg.Wait()

	return nil
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
