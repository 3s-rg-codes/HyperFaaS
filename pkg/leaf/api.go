package leafv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/controlplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane"
	dpnet "github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane/net"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	leafproxy "github.com/3s-rg-codes/HyperFaaS/pkg/leaf/proxy"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
)

const STATE_STREAM_BUFFER = 10000

const INSTANCE_CHANGES_CHANNEL_BUFFER = 10000

const METRIC_CHANNEL_BUFFER = 10000

// Server implements the leafpb.LeafServer gRPC.
type Server struct {
	leafpb.UnimplementedLeafServer

	cfg    config.Config
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	workers []*dataplane.WorkerClient
	// the id of this node. used to subscribe to status stream of workers.
	nodeID string

	metadataClient metadata.Client

	dataPlane *dataplane.DataPlane

	controlPlane *controlplane.ControlPlane

	// reporter used to trigger scaling decisions.
	// the direction of communication here is DataPlane / API (ScheduleCall) -> ControlPlane.
	// Used for example in the scale from zero situation.
	concurrencyReporter *metrics.ConcurrencyReporter

	// single centralised channel to read zero scale events from all functions.
	// used for the State stream.
	functionScaleEvents chan metrics.ZeroScaleEvent
}

// NewServer initialises a leaf server with the provided configuration and logger.
func NewServer(ctx context.Context, cfg config.Config, metadataClient metadata.Client, logger *slog.Logger) (*Server, error) {
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
	// create worker clients
	workers := make([]*dataplane.WorkerClient, 0, len(cfg.WorkerAddresses))
	for idx, addr := range cfg.WorkerAddresses {
		h, err := dataplane.NewWorkerClient(serverCtx, idx, addr, cfg, logger.With("worker", addr))
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

func (s *Server) bootstrapMetadata() error {
	list, err := s.metadataClient.ListFunctions(s.ctx)
	if err != nil {
		return fmt.Errorf("list function metadata: %w", err)
	}
	count := 0
	var revision int64
	if list != nil {
		for _, fn := range list.Functions {
			s.upsertFunction(fn)
			count++
		}
		revision = list.Revision
	} else {
		revision = 0
	}

	events, errs := s.metadataClient.WatchFunctions(s.ctx, revision)
	go s.watchMetadata(events, errs)

	if count > 0 {
		s.logger.Info("initialised function controllers from metadata", "count", count)
	}
	return nil
}

func (s *Server) watchMetadata(events <-chan metadata.Event, errs <-chan error) {
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
			s.logger.Error("metadata delete without function id")
			return
		}
		s.removeFunction(ev.FunctionID)
	default:
		s.logger.Error("unknown metadata event", "type", ev.Type)
	}
}

func (s *Server) upsertFunction(meta *metadata.FunctionMetadata) {
	if meta == nil {
		return
	}
	if meta.ID == "" {
		s.logger.Error("metadata missing function id")
		return
	}
	if meta.Config == nil {
		s.logger.Error("metadata missing config", "functionID", meta.ID)
		return
	}

	s.controlPlane.UpsertFunction(meta)
}

func (s *Server) removeFunction(functionID string) {
	s.dataPlane.RemoveThrottler(functionID)
	s.controlPlane.RemoveFunction(functionID)
}

// State streams changes in state of a function_id.
// A change in the number of running instances.
// IMPORTANT: this is meant for only ONE client to be listening to.
// IMPORTANT: this does not handle additional functions being registered after the stream is established.
// To get the latest state of all functions, the client must call this again.
func (s *Server) State(req *common.StateRequest, stream leafpb.Leaf_StateServer) error {
	ctx := stream.Context()

	for event := range s.functionScaleEvents {
		s.logger.Debug("received zero scale event", "function_id", event.FunctionId, "zero", event.Have)
		// exit if context is done
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := stream.Send(&common.StateResponse{
			FunctionId: event.FunctionId,
			Have:       event.Have,
		})
		if err != nil {
			s.logger.Error("failed to send state response", "function_id", event.FunctionId, "error", err)
			return err
		}
	}

	return nil
}

func (s *Server) handleWorkerStatus(workerIdx int, update *dataplane.WorkerStatusEvent) {
	if update == nil || update.FunctionId == "" {
		return
	}
	s.controlPlane.HandleWorkerEvent(workerIdx, update)
}

// ProxyBackendResolver exposes a BackendResolver that can be used by the gRPC proxy listener.
func (s *Server) ProxyBackendResolver() leafproxy.BackendResolver {
	return leafproxy.BackendResolverFunc(func(ctx context.Context, functionID string, fullMethodName string) (grpc.ClientConnInterface, error) {
		if functionID == "" {
			return nil, status.Error(codes.InvalidArgument, "function id is required for proxy calls")
		}

		if !s.controlPlane.FunctionExists(functionID) {
			return nil, status.Errorf(codes.NotFound, "function %s not found in control plane", functionID)
		}
		s.logger.Debug("leasing connection for function", "function_id", functionID)
		conn, release, err := s.dataPlane.LeaseConnection(ctx, functionID)
		if err != nil {
			if errors.Is(err, dpnet.ErrNoInstancesAvailable) {
				return nil, status.Errorf(codes.Unavailable, "function %s has no ready instances", functionID)
			}
			return nil, status.Errorf(codes.Unavailable, "failed to route %s: %v", functionID, err)
		}
		s.logger.Debug("wrapped connection for function", "function_id", functionID)
		return leafproxy.WrapClientConn(conn, release), nil
	})
}
