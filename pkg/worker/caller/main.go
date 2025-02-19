package caller

import (
	"context"
	"log/slog"
	"net"
	"sync"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	FunctionCalls     FunctionCalls
	FunctionResponses FunctionResponses
	logger            *slog.Logger
}

type FunctionCalls struct {
	FcMap map[string]chan []byte
	mu    sync.RWMutex
}

type FunctionResponses struct {
	FrMap map[string]chan *Response
	mu    sync.RWMutex
}

type Response struct {
	Data  []byte
	Error []byte
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {
	// Pass payload to the functionResponses channel IF it exists
	if !payload.FirstExecution {
		s.logger.Debug("Passing response", "response", payload.Data, "instance ID", payload.Id)
		go s.PushResponseToChannel(payload.Id, &Response{Data: payload.Data}) //TODO
	}

	// Wait for the function to be called
	s.logger.Debug("Looking at channel for a call", "instance ID", payload.Id)
	channel := s.ExtractCallFromChannel(payload.Id)
	if channel == nil {
		s.logger.Error("Channel not found for", "id", payload.Id)
		return nil, status.Error(codes.NotFound, "Channel not found")
	}

	call, ok := <-channel

	if !ok {
		s.logger.Error("Channel closed", "instance ID", payload.Id)
		return nil, status.Error(codes.Internal, "Channel closed")
	}

	s.logger.Debug("Received call", "call", call)

	// Send the call to the function instance
	return &pb.Call{Data: call, Id: payload.Id}, nil
}

func (s *CallerServer) Start() {
	lis, err := net.Listen("tcp", ":50052")
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

func NewCallerServer(logger *slog.Logger) *CallerServer {
	return &CallerServer{
		logger: logger,
		FunctionCalls: FunctionCalls{
			FcMap: make(map[string]chan []byte),
		},
		FunctionResponses: FunctionResponses{
			FrMap: make(map[string]chan *Response),
		},
	}
}

// RegisterFunction adds message channels for the given function ID
func (s *CallerServer) RegisterFunction(id string) {
	s.FunctionCalls.mu.Lock()
	defer s.FunctionCalls.mu.Unlock()
	s.FunctionCalls.FcMap[id] = make(chan []byte, 1)

	s.FunctionResponses.mu.Lock()
	defer s.FunctionResponses.mu.Unlock()
	s.FunctionResponses.FrMap[id] = make(chan *Response, 1)
}

func (s *CallerServer) UnregisterFunction(id string) {
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

func (s *CallerServer) PassCallToChannel(id string, call []byte) {
	s.FunctionCalls.mu.RLock()
	defer s.FunctionCalls.mu.RUnlock()
	if ch, ok := s.FunctionCalls.FcMap[id]; ok {
		ch <- call
	}
}

func (s *CallerServer) ExtractCallFromChannel(id string) chan []byte {
	s.FunctionCalls.mu.RLock()
	ch, ok := s.FunctionCalls.FcMap[id]
	s.FunctionCalls.mu.RUnlock()

	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) ExtractResponseFromChannel(id string) chan *Response {
	s.FunctionResponses.mu.RLock()
	ch, ok := s.FunctionResponses.FrMap[id]
	s.FunctionResponses.mu.RUnlock()
	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) PushResponseToChannel(id string, response *Response) {
	s.FunctionResponses.mu.RLock()
	defer s.FunctionResponses.mu.RUnlock()
	if ch, ok := s.FunctionResponses.FrMap[id]; ok {
		ch <- response
	}
}
