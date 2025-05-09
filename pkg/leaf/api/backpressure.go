package api

import (
	"context"
	"log"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

// IsFunctionBackpressured checks if the worker has too many instances starting or running for a function.
// Returns true if the worker is under backpressure.
func (s *LeafServer) IsFunctionBackpressured(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID) (bool, error) {
	// Check if we already have too many instances starting
	startingCount, err := s.scheduler.GetInstanceCount(workerID, functionID, state.InstanceStateStarting)
	if err != nil {
		return false, err
	}

	if startingCount >= s.maxStartingInstancesPerFunction {
		log.Printf("Backpressure !! Already %d instances starting for function %s",
			startingCount, functionID)
		return true, nil
	}

	// Check if we already have too many instances running
	runningCount, err := s.scheduler.GetInstanceCount(workerID, functionID, state.InstanceStateRunning)
	if err != nil {
		return false, err
	}

	if runningCount >= s.maxRunningInstancesPerFunction {
		log.Printf("Backpressure !! Already %d instances running for function %s",
			runningCount, functionID)
		return true, nil
	}

	return false, nil
}
