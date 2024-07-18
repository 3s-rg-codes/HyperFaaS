package caller

import (
	"context"
	"net"
	"sync"

	"github.com/rs/zerolog/log"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
)

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	FunctionCalls     FunctionCalls
	FunctionResponses FunctionResponses
}

type FunctionCalls struct {
	FcMap map[string]chan string
	mu    sync.RWMutex
}

type FunctionResponses struct {
	FrMap map[string]chan string
	mu    sync.RWMutex
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {
	// Pass payload to the functionResponses channel IF it exists
	if !payload.FirstExecution {
		log.Debug().Msgf("Passing response [%v] to channel with instance ID %s", payload.Data, payload.Id)
		go s.PushResponseToChannel(payload.Id, payload.Data)
	}

	// Wait for the function to be called
	log.Debug().Msgf("Looking at channel for a call with instance ID %s", payload.Id)
	call := <-s.ExtractCallFromChannel(payload.Id)
	log.Debug().Msgf("Received call: %s", call)

	// Send the call to the function instance
	return &pb.Call{Data: call, Id: payload.Id}, nil
}

func (s *CallerServer) Start() {
	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Error().Msgf("failed to listen: %v", err)
		return
	}

	grpcServer := grpc.NewServer()
	pb.RegisterFunctionServiceServer(grpcServer, s)

	log.Debug().Msgf("Caller Server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Error().Msgf("CallerServer failed to serve: %v", err)
	}
	defer grpcServer.Stop()
}

func New() CallerServer {
	return CallerServer{
		FunctionCalls: FunctionCalls{
			FcMap: make(map[string]chan string),
		},
		FunctionResponses: FunctionResponses{
			FrMap: make(map[string]chan string),
		},
	}
}

// RegisterFunction adds message channels for the given function ID
func (s *CallerServer) RegisterFunction(id string) {
	s.FunctionCalls.mu.Lock()
	defer s.FunctionCalls.mu.Unlock()
	s.FunctionCalls.FcMap[id] = make(chan string)

	s.FunctionResponses.mu.Lock()
	defer s.FunctionResponses.mu.Unlock()
	s.FunctionResponses.FrMap[id] = make(chan string)
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

func (s *CallerServer) PassCallToChannel(id string, call string) {
	s.FunctionCalls.mu.RLock()
	defer s.FunctionCalls.mu.RUnlock()
	if ch, ok := s.FunctionCalls.FcMap[id]; ok {
		ch <- call
	}
}

func (s *CallerServer) ExtractCallFromChannel(id string) <-chan string {
	s.FunctionCalls.mu.RLock()
	ch, ok := s.FunctionCalls.FcMap[id]
	s.FunctionCalls.mu.RUnlock()

	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) ExtractResponseFromChannel(id string) <-chan string {
	s.FunctionResponses.mu.RLock()
	ch, ok := s.FunctionResponses.FrMap[id]
	s.FunctionResponses.mu.RUnlock()
	if ok {
		return ch
	}
	return nil
}

func (s *CallerServer) PushResponseToChannel(id string, response string) {
	s.FunctionResponses.mu.RLock()
	defer s.FunctionResponses.mu.RUnlock()
	if ch, ok := s.FunctionResponses.FrMap[id]; ok {
		ch <- response
	}
}
