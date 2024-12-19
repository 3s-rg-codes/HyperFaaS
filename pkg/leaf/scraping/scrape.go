package scraping

import (
	"context"
	"log/slog"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Scraper interface {
	// Scrape the state of all workers
	Scrape(ctx context.Context) (scheduling.WorkerStateMap, error)
	// A single worker's state
	GetWorkerState(workerIP string) ([]scheduling.FunctionState, error)
	// Set the worker IPs to scrape
	SetWorkerIPs(workerIPs []string)
}

var workerIPs = []string{}

const (
	// TODO use real id
	leafLeaderID = "leafLeader"
	timeout      = 30 * time.Second
)

type scraper struct {
	workerIPs         []string
	workerConnections map[string]pb.ControllerClient
	// cache of the current state of the workers
	scrapeInterval time.Duration
	state          scheduling.WorkerStateMap
	logger         *slog.Logger
}

func NewScraper(scrapeInterval time.Duration, logger *slog.Logger) Scraper {
	return &scraper{
		scrapeInterval: scrapeInterval,
		logger:         logger,
	}
}

func (s *scraper) Scrape(ctx context.Context) (scheduling.WorkerStateMap, error) {
	for _, workerIP := range s.workerIPs {
		workerState, err := s.GetWorkerState(workerIP)
		if err != nil {
			return nil, err
		}
		s.state[workerIP] = workerState
		s.logger.Debug("Scraped worker state", "workerIP", workerIP, "state", workerState)
	}
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

func (s *scraper) GetWorkerState(workerIP string) ([]scheduling.FunctionState, error) {
	if s.workerConnections[workerIP] == nil {
		conn, err := grpc.NewClient(workerIP, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		// store the connection in the map
		s.workerConnections[workerIP] = pb.NewControllerClient(conn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	state, err := s.workerConnections[workerIP].State(ctx, &pb.StateRequest{
		NodeId: leafLeaderID,
	})
	if err != nil {
		return nil, err
	}

	return convertStateResponseToWorkerState(state), nil
}

// convertStateResponseToWorkerState converts a StateResponse to []scheduling.FunctionState
func convertStateResponseToWorkerState(state *pb.StateResponse) []scheduling.FunctionState {
	workerState := make([]scheduling.FunctionState, len(state.Functions))
	for i, function := range state.Functions {
		// Convert running instances
		runningInstances := make([]scheduling.InstanceState, len(function.Running))
		for j, instance := range function.Running {
			runningInstances[j] = scheduling.InstanceState{
				InstanceID:        instance.InstanceId,
				IsActive:          instance.IsActive,
				TimeSinceLastWork: time.Duration(instance.TimeSinceLastWork) * time.Millisecond,
				Uptime:            time.Duration(instance.Uptime) * time.Millisecond,
			}
		}

		// Convert idle instances
		idleInstances := make([]scheduling.InstanceState, len(function.Idle))
		for j, instance := range function.Idle {
			idleInstances[j] = scheduling.InstanceState{
				InstanceID:        instance.InstanceId,
				IsActive:          instance.IsActive,
				TimeSinceLastWork: time.Duration(instance.TimeSinceLastWork) * time.Millisecond,
				Uptime:            time.Duration(instance.Uptime) * time.Millisecond,
			}
		}

		workerState[i] = scheduling.FunctionState{
			FunctionID: function.FunctionId,
			Running:    runningInstances,
			Idle:       idleInstances,
		}
	}
	return workerState
}
