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
	BUFFER_SIZE = 1000
)

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	Address           string
	FunctionCalls     InstanceCalls
	FunctionResponses InstanceResponses
	StatsManager      *stats.StatsManager
	logger            *slog.Logger
}

type InstanceCalls struct {
	FcMap map[string]chan []byte
	mu    sync.RWMutex
}

type InstanceResponses struct {
	FrMap map[string]chan []byte
	mu    sync.RWMutex
}

func NewCallerServer(address string, logger *slog.Logger, statsManager *stats.StatsManager) *CallerServer {
	return &CallerServer{
		Address:      address,
		logger:       logger,
		StatsManager: statsManager,
		FunctionCalls: InstanceCalls{
			FcMap: make(map[string]chan []byte),
		},
		FunctionResponses: InstanceResponses{
			FrMap: make(map[string]chan []byte),
		},
	}
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {

	// Pass payload to the functionResponses channel IF it exists
	if !payload.FirstExecution {
		s.logger.Debug("Passing response", "response", payload.Data, "instance ID", payload.InstanceId)
		go s.QueueInstanceResponse(payload.InstanceId.Id, payload.Data)
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
	s.FunctionCalls.mu.Lock()
	s.FunctionCalls.FcMap[id] = make(chan []byte, BUFFER_SIZE)
	s.FunctionCalls.mu.Unlock()

	s.FunctionResponses.mu.Lock()
	s.FunctionResponses.FrMap[id] = make(chan []byte, BUFFER_SIZE)
	s.FunctionResponses.mu.Unlock()
}

// UnregisterFunctionInstance closes and removes message channels for the given function ID
func (s *CallerServer) UnregisterFunctionInstance(id string) {
	s.FunctionCalls.mu.Lock()
	if _, ok := s.FunctionCalls.FcMap[id]; ok {
		close(s.FunctionCalls.FcMap[id])
		delete(s.FunctionCalls.FcMap, id)
	}
	s.FunctionCalls.mu.Unlock()

	s.FunctionResponses.mu.Lock()
	if _, ok := s.FunctionResponses.FrMap[id]; ok {
		close(s.FunctionResponses.FrMap[id])
		delete(s.FunctionResponses.FrMap, id)
	}
	s.FunctionResponses.mu.Unlock()
}

func (s *CallerServer) QueueInstanceCall(id string, call []byte) {
	s.FunctionCalls.mu.RLock() // Use Read Lock to find the channel
	ch, ok := s.FunctionCalls.FcMap[id]
	if ok {
		s.logger.Debug("Queueing call data", "instance ID", id)
		ch <- call
		s.logger.Debug("Finished queueing call data", "instance ID", id)
	} else {
		s.logger.Warn("Attempted to queue call for unregistered/removed instance", "instanceId", id)
	}
	s.FunctionCalls.mu.RUnlock()

}

func (s *CallerServer) GetInstanceCall(id string) chan []byte {
	s.FunctionCalls.mu.RLock()
	ch, ok := s.FunctionCalls.FcMap[id]
	s.FunctionCalls.mu.RUnlock()

	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) GetInstanceResponse(id string) chan []byte {
	s.FunctionResponses.mu.RLock()
	ch, ok := s.FunctionResponses.FrMap[id]
	s.FunctionResponses.mu.RUnlock()
	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) QueueInstanceResponse(id string, response []byte) {
	s.FunctionResponses.mu.RLock()
	ch, ok := s.FunctionResponses.FrMap[id]
	if ok {
		s.logger.Debug("Queueing response data", "instance ID", id)
		ch <- response
		s.logger.Debug("Finished queueing response data", "instance ID", id)
	} else {
		s.logger.Warn("Attempted to queue response for unregistered/removed instance", "instanceId", id)
	}
	s.FunctionResponses.mu.RUnlock()

}
