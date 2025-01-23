package state

import (
	"context"
	"log/slog"
	"time"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
)

type WorkerID string
type InstanceID string
type FunctionID string

type WorkerState int
type InstanceState int

const (
	WorkerStateUp WorkerState = iota
	WorkerStateDown
)

const (
	InstanceStateRunning InstanceState = iota
	InstanceStateIdle
	InstanceStateNew
)

// WorkerStateMap maps worker IPs to their registered functions
type WorkerStateMap map[WorkerID][]Function

// Function represents the state of a function and its instances
type Function struct {
	FunctionID FunctionID
	Running    []Instance
	Idle       []Instance
}

// Instance represents the state of a single function instance
type Instance struct {
	InstanceID InstanceID
	IsActive   bool
	LastWorked time.Time
	Created    time.Time
}

type Scraper interface {
	// Scrape the state of all workers
	Scrape(ctx context.Context) (WorkerStateMap, error)
	// A single worker's state
	GetWorkerState(workerID WorkerID) ([]Function, error)
	// Set the worker IDs to scrape
	SetWorkerIDs(workerIDs []WorkerID)
}

const (
	// TODO use real id
	leafLeaderID = "leafLeader"
	timeout      = 30 * time.Second
)

type scraper struct {
	workerIDs         []WorkerID
	workerConnections map[WorkerID]pb.ControllerClient
	// cache of the current state of the workers
	scrapeInterval time.Duration
	state          WorkerStateMap
	logger         *slog.Logger
}

func NewScraper(scrapeInterval time.Duration, logger *slog.Logger) Scraper {
	return &scraper{
		scrapeInterval: scrapeInterval,
		logger:         logger,
	}
}

func (s *scraper) Scrape(ctx context.Context) (WorkerStateMap, error) {
	for _, workerID := range s.workerIDs {
		workerState, err := s.GetWorkerState(workerID)
		if err != nil {
			return nil, err
		}
		s.state[workerID] = workerState
		s.logger.Debug("Scraped worker state", "workerID", workerID, "state", workerState)
	}
	return s.state, nil
}

func (s *scraper) SetWorkerIDs(workerIDs []WorkerID) {
	copy(s.workerIDs, workerIDs)
}

func New(workerIDs []WorkerID) Scraper {
	s := &scraper{
		workerIDs: make([]WorkerID, len(workerIDs)),
		state:     make(WorkerStateMap),
	}
	copy(s.workerIDs, workerIDs)

	// Initialize state map for each worker
	for _, workerID := range s.workerIDs {
		s.state[workerID] = make([]Function, 0)
	}
	return s
}

func (s *scraper) GetWorkerState(workerID WorkerID) ([]Function, error) {
	if s.workerConnections[workerID] == nil {
		conn, err := grpc.NewClient(string(workerID))
		if err != nil {
			return nil, err
		}
		// store the connection in the map
		s.workerConnections[workerID] = pb.NewControllerClient(conn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	state, err := s.workerConnections[workerID].State(ctx, &pb.StateRequest{
		NodeId: leafLeaderID,
	})
	if err != nil {
		return nil, err
	}

	return convertStateResponseToWorkerState(state), nil
}

// convertStateResponseToWorkerState converts a StateResponse to []FunctionState
// This honestly seems like an antipattern. We convert this to our local type bc one shouldnt copy the proto types.
func convertStateResponseToWorkerState(state *pb.StateResponse) []Function {
	workerState := make([]Function, len(state.Functions))
	for i, function := range state.Functions {
		// Convert running instances
		runningInstances := make([]Instance, len(function.Running))
		for j, instance := range function.Running {
			runningInstances[j] = Instance{
				InstanceID: InstanceID(instance.InstanceId),
				IsActive:   instance.IsActive,
				LastWorked: instance.Lastworked.AsTime(),
				Created:    instance.Created.AsTime(),
			}
		}

		// Convert idle instances
		idleInstances := make([]Instance, len(function.Idle))
		for j, instance := range function.Idle {
			idleInstances[j] = Instance{
				InstanceID: InstanceID(instance.InstanceId),
				IsActive:   instance.IsActive,
				LastWorked: instance.Lastworked.AsTime(),
				Created:    instance.Created.AsTime(),
			}
		}

		workerState[i] = Function{
			FunctionID: FunctionID(function.FunctionId),
			Running:    runningInstances,
			Idle:       idleInstances,
		}
	}
	return workerState
}
