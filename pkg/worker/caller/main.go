package caller

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	BUFFER_SIZE = 100000
)

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	Address           string
	FunctionCalls     InstanceCalls
	FunctionResponses InstanceResponses
	StatsManager      *stats.StatsManager
	logger            *slog.Logger
	mu                sync.RWMutex
}

type InstanceCalls struct {
	FcMap sync.Map
}

type InstanceResponses struct {
	FrMap sync.Map
}

func NewCallerServer(address string, logger *slog.Logger, statsManager *stats.StatsManager) *CallerServer {
	return &CallerServer{
		Address:           address,
		logger:            logger,
		StatsManager:      statsManager,
		FunctionCalls:     InstanceCalls{},
		FunctionResponses: InstanceResponses{},
	}
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {
	// Pass payload to the functionResponses channel IF it exists
	if !payload.FirstExecution {
		s.logger.Debug("Passing response", "response", payload.Data, "instance ID", payload.InstanceId)
		s.QueueInstanceResponse(payload.InstanceId.Id, payload.Data)
		// TODO: Generate event of response
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
		return &pb.Call{Data: call, InstanceId: payload.InstanceId}, nil
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
	s.FunctionCalls.FcMap.Store(id, make(chan []byte))
	s.FunctionResponses.FrMap.Store(id, make(chan []byte))
}

// UnregisterFunctionInstance closes and removes message channels for the given function ID
func (s *CallerServer) UnregisterFunctionInstance(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.FunctionCalls.FcMap.Load(id); ok {
		close(ch.(chan []byte))
		s.FunctionCalls.FcMap.Delete(id)
	}

	if ch, ok := s.FunctionResponses.FrMap.Load(id); ok {
		close(ch.(chan []byte))
		s.FunctionResponses.FrMap.Delete(id)
	}
}

func (s *CallerServer) QueueInstanceCall(id string, call []byte) {
	if ch, ok := s.FunctionCalls.FcMap.Load(id); ok {
		s.logger.Debug("Queueing call data", "instance ID", id)
		ch.(chan []byte) <- call
		s.logger.Debug("Finished queueing call data", "instance ID", id)
	} else {
		s.logger.Warn("Attempted to queue call for unregistered/removed instance", "instanceId", id)
	}
}

func (s *CallerServer) GetInstanceCall(id string) chan []byte {
	if ch, ok := s.FunctionCalls.FcMap.Load(id); ok {
		return ch.(chan []byte)
	}
	return nil
}

func (s *CallerServer) GetInstanceResponse(id string) chan []byte {
	if ch, ok := s.FunctionResponses.FrMap.Load(id); ok {
		return ch.(chan []byte)
	}
	return nil
}

func (s *CallerServer) QueueInstanceResponse(id string, response []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ch, ok := s.FunctionResponses.FrMap.Load(id); ok {
		ch.(chan []byte) <- response
	} else {
		s.logger.Warn("Attempted to queue response for unregistered/removed instance", "instanceId", id)
	}
}
