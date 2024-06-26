package caller

import (
	"context"
	"errors"
	"net"

	"github.com/rs/zerolog/log"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
)

type CallerServer struct {
	pb.UnimplementedFunctionServiceServer
	FunctionCalls     map[string]chan string
	FunctionResponses map[string]chan string
}

func (s *CallerServer) Ready(ctx context.Context, payload *pb.Payload) (*pb.Call, error) {

	//Pass payload to the functionResponses channel IF it exists
	if payload.Data != "" {

		log.Debug().Msgf("Passing response [%v] to channel with instance ID %s", payload.Data, payload.Id)

		go func() {
			s.FunctionResponses[payload.Id] <- payload.Data
		}()

	}

	//Wait for the function to be called
	log.Debug().Msgf("Looking at channel for a call with instance ID %s", payload.Id)

	call, ok := <-s.FunctionCalls[payload.Id]
	if !ok {

		log.Debug().Msgf("Channel for instance ID %s was closed while waiting for call", payload.Id)

		return nil, errors.New("channel was closed")
	}

	log.Debug().Msgf("Received call: %s", call)

	//Send the call to the function instance
	return &pb.Call{Data: call, Id: payload.Id}, nil

}

func (s *CallerServer) Start() {
	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Error().Msgf("failed to listen: %v", err)
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
		FunctionCalls:     make(map[string]chan string),
		FunctionResponses: make(map[string]chan string),
	}
}

// This function adds message channels for the given function ID
func (s *CallerServer) RegisterFunction(id string) {

	s.FunctionCalls[id] = make(chan string)

	s.FunctionResponses[id] = make(chan string)
}

func (s *CallerServer) UnregisterFunction(id string) {
	//if the function is registered, close the channels and delete the function
	if _, ok := s.FunctionCalls[id]; !ok {
		return
	}

	close(s.FunctionCalls[id])

	close(s.FunctionResponses[id])

	delete(s.FunctionCalls, id)

	delete(s.FunctionResponses, id)
}
