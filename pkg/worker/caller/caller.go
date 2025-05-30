package caller

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	BUFFER_SIZE = 100000
)

type Request struct {
	Context context.Context
	Payload []byte
}

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	Address           string
	functionCalls     map[string]chan Request
	functionResponses map[string]chan Request
	callsMu           sync.RWMutex
	responsesMu       sync.RWMutex
	StatsManager      *stats.StatsManager
	logger            *slog.Logger
	mu                sync.RWMutex
}

func NewCallerServer(address string, logger *slog.Logger, statsManager *stats.StatsManager) *CallerServer {
	return &CallerServer{
		Address:           address,
		logger:            logger,
		StatsManager:      statsManager,
		functionCalls:     make(map[string]chan Request),
		functionResponses: make(map[string]chan Request),
	}
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {
	// Pass payload to the functionResponses channel IF it exists
	if !payload.FirstExecution {
		s.logger.Debug("Passing response", "response", payload.Data, "instance ID", payload.InstanceId)
		gotResponseTimestamp := time.Now()

		s.QueueInstanceResponse(payload.InstanceId.Id, Request{Context: context.WithValue(ctx, "gotResponseTimestamp", gotResponseTimestamp), Payload: payload.Data})
	} else {
		// To measure cold start time
		s.StatsManager.Enqueue(stats.Event().Function(payload.FunctionId.Id).Container(payload.InstanceId.Id).Running().Success())
	}

	callChan := s.GetInstanceCall(payload.InstanceId.Id)
	if callChan == nil {
		s.logger.Error("Channel not found", "instance ID", payload.InstanceId)
		return nil, status.Error(codes.NotFound, "Function channel not found")
	}

	// Wait for the function to be called
	s.logger.Debug("Looking at channel for a call", "instance ID", payload.InstanceId)
	select {
	case call, ok := <-callChan:
		if !ok {
			s.logger.Error("Channel closed", "instance ID", payload.InstanceId)
			return nil, status.Error(codes.Unavailable, "Function channel was closed")
		}
		s.logger.Debug("Received call", "call", call)
		return &pb.Call{Data: call.Payload, InstanceId: payload.InstanceId}, nil
	case <-ctx.Done():
		s.logger.Info("Instance timed out waiting for call", "function ID", payload.FunctionId, "instance ID", payload.InstanceId)
		s.StatsManager.Enqueue(stats.Event().Function(payload.FunctionId.Id).Container(payload.InstanceId.Id).Timeout())
		s.UnregisterFunctionInstance(payload.InstanceId.Id)
		return nil, nil
	}
}

func (s *CallerServer) Start() {
	lis, err := net.Listen("tcp", s.Address)
	if err != nil {
		s.logger.Error("Failed to listen", "error", err)
		return
	}

	grpcServer := grpc.NewServer()
	pb.RegisterFunctionServiceServer(grpcServer, s)

	s.logger.Debug("Caller Server listening", "address", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		s.logger.Error("Failed to serve", "error", err)
	}
	defer grpcServer.Stop()
}

// RegisterFunctionInstance adds message channels for the given function ID
func (s *CallerServer) RegisterFunctionInstance(id string) {
	s.callsMu.Lock()
	s.functionCalls[id] = make(chan Request)
	s.callsMu.Unlock()

	s.responsesMu.Lock()
	s.functionResponses[id] = make(chan Request)
	s.responsesMu.Unlock()
}

// UnregisterFunctionInstance closes and removes message channels for the given function ID
func (s *CallerServer) UnregisterFunctionInstance(id string) {
	s.callsMu.Lock()
	if ch, ok := s.functionCalls[id]; ok {
		close(ch)
		delete(s.functionCalls, id)
	}
	s.callsMu.Unlock()

	s.responsesMu.Lock()
	if ch, ok := s.functionResponses[id]; ok {
		close(ch)
		delete(s.functionResponses, id)
	}
	s.responsesMu.Unlock()
}

func (s *CallerServer) QueueInstanceCall(id string, call Request) {
	s.callsMu.RLock()
	ch, ok := s.functionCalls[id]
	s.callsMu.RUnlock()

	if ok {
		s.logger.Debug("Queueing call data", "instance ID", id)
		ch <- call
		s.logger.Debug("Finished queueing call data", "instance ID", id)
	} else {
		s.logger.Warn("Attempted to queue call for unregistered/removed instance", "instanceId", id)
	}
}

func (s *CallerServer) GetInstanceCall(id string) chan Request {
	s.callsMu.RLock()
	defer s.callsMu.RUnlock()
	ch, ok := s.functionCalls[id]
	if !ok {
		return nil
	}
	return ch
}

func (s *CallerServer) GetInstanceResponse(id string) chan Request {
	s.responsesMu.RLock()
	defer s.responsesMu.RUnlock()
	ch, ok := s.functionResponses[id]
	if !ok {
		return nil
	}
	return ch
}

func (s *CallerServer) QueueInstanceResponse(id string, response Request) {
	s.responsesMu.RLock()
	ch, ok := s.functionResponses[id]
	s.responsesMu.RUnlock()

	if ok {
		ch <- response
	} else {
		s.logger.Warn("Attempted to queue response for unregistered/removed instance", "instanceId", id)
	}
}
