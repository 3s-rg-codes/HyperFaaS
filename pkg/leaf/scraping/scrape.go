package scraping

import (
	"context"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Scraper interface {
	Scrape(ctx context.Context) (scheduling.WorkerStateMap, error)
	SetWorkerIPs(workerIPs []string)
}

var workerIPs = []string{}

const (
	scrapeInterval = 50 * time.Millisecond
	// TODO use real id
	leafLeaderID = "leafLeader"
)

type scraper struct {
	workerIPs         []string
	workerConnections map[string]*pb.ControllerClient
	// cache of the current state of the workers
	state scheduling.WorkerStateMap
}

func (s *scraper) Scrape(ctx context.Context) (scheduling.WorkerStateMap, error) {
	return s.state, nil
}

func (s *scraper) SetWorkerIPs(workerIPs []string) {
	copy(s.workerIPs, workerIPs)
}

func New(workerIPs []string) Scraper {
	s := &scraper{
		workerIPs: make([]string, len(workerIPs)),
		state:     make(scheduling.WorkerStateMap),
	}
	copy(s.workerIPs, workerIPs)

	// Initialize state map for each worker
	for _, workerIP := range s.workerIPs {
		s.state[workerIP] = make([]scheduling.FunctionState, 0)
	}

	return s
}

// TODO: Implement the scraping logic
// Scrape the workers and update the state
// This will be called periodically by the leafLeader
// And build the basis for the scheduling decision
func (s *scraper) getWorkerState(workerIP string) ([]scheduling.FunctionState, error) {
	if s.workerConnections[workerIP] == nil {
		conn, err := grpc.NewClient(workerIP, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		// store the connection in the map
		s.workerConnections[workerIP] = pb.NewControllerClient(conn)
	}

	state, err := s.workerConnections[workerIP].State(ctx, &hyperfaas.StateRequest{
		NodeID: leafLeaderID,
	})
	return nil, nil
}
