package api

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

// InstanceCoordinator coordinates instance creation across multiple concurrent calls
type InstanceCoordinator struct {
	functionLocks   map[state.FunctionID]*sync.Mutex
	functionsMutex  sync.RWMutex
	backoff         time.Duration
	maxBackoff      time.Duration
	backoffIncrease time.Duration
}

func NewInstanceCoordinator(backoff time.Duration, maxBackoff time.Duration, backoffIncrease time.Duration) *InstanceCoordinator {
	return &InstanceCoordinator{
		functionLocks:   make(map[state.FunctionID]*sync.Mutex),
		backoff:         backoff,
		maxBackoff:      maxBackoff,
		backoffIncrease: backoffIncrease,
	}
}

// getFunctionLock gets or creates a mutex for a specific function
func (c *InstanceCoordinator) getFunctionLock(functionID state.FunctionID) *sync.Mutex {
	c.functionsMutex.RLock()
	lock, exists := c.functionLocks[functionID]
	c.functionsMutex.RUnlock()

	if exists {
		return lock
	}

	c.functionsMutex.Lock()
	defer c.functionsMutex.Unlock()

	// Double-check to avoid race condition
	if lock, exists = c.functionLocks[functionID]; exists {
		return lock
	}

	lock = &sync.Mutex{}
	c.functionLocks[functionID] = lock
	return lock
}

// CoordinateInstanceCreation manages instance creation with backpressure
func (c *InstanceCoordinator) CoordinateInstanceCreation(
	ctx context.Context,
	functionID state.FunctionID,
	checkBackpressure func(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID) (bool, error),
	schedule func(ctx context.Context, functionID state.FunctionID) (state.WorkerID, state.InstanceID, error),
	startInstance func(ctx context.Context, workerID state.WorkerID, functionID state.FunctionID) (state.InstanceID, error),
	updateInstanceState func(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID, state state.InstanceState),
	startingInstanceWaitTimeout time.Duration,
) (state.WorkerID, state.InstanceID, error) {
	lock := c.getFunctionLock(functionID)

	// first try to schedule without lock
	workerID, instanceID, err := schedule(ctx, functionID)
	if err != nil {
		return "", "", err
	}

	// If we found an idle instance, use it (best case)
	if instanceID != "" {
		updateInstanceState(workerID, functionID, instanceID, state.InstanceStateRunning)
		return workerID, instanceID, nil
	}

	// No idle instance found, we need to create one or wait - acquire FUNCTION lock
	lock.Lock()
	defer lock.Unlock()

	// Try scheduling again in case something became available while waiting for lock
	workerID, instanceID, err = schedule(ctx, functionID)
	if err != nil {
		return "", "", err
	}

	if instanceID != "" {
		updateInstanceState(workerID, functionID, instanceID, state.InstanceStateRunning)
		return workerID, instanceID, nil
	}

	// Check if we're under backpressure
	backpressured, err := checkBackpressure(ctx, workerID, functionID)
	if err != nil {
		return "", "", err
	}

	if backpressured {
		// We're under backpressure, wait and retry scheduling
		//log.Printf("Function %s is under backpressure, waiting for available instances", functionID)

		// there is potential for optimization here (the timings used in this function)
		deadline := time.Now().Add(startingInstanceWaitTimeout)

		for time.Now().Before(deadline) {
			if c.backoff < c.maxBackoff {
				time.Sleep(c.backoff)
				c.backoff += c.backoffIncrease
			} else {
				time.Sleep(c.maxBackoff)
			}

			// Try to schedule again
			workerID, instanceID, err = schedule(ctx, functionID)
			if err != nil {
				return "", "", err
			}

			if instanceID != "" {
				updateInstanceState(workerID, functionID, instanceID, state.InstanceStateRunning)
				return workerID, instanceID, nil
			}

			// Check backpressure again
			backpressured, err = checkBackpressure(ctx, workerID, functionID)
			if err != nil {
				return "", "", err
			}

			if !backpressured {
				break // Exit waiting loop if no longer under backpressure
			}
		}

		// If we're still backpressured but timeout reached, proceed anyway
		// so the request doesn't hang indefinitely
		// TODO: find a better solution?
	}

	// Start a new instance since we're not under backpressure or we timed out waiting
	instanceID, err = startInstance(ctx, workerID, functionID)
	if err != nil {
		return "", "", err
	}

	log.Printf("Coordinator started new instance for function %s on worker %s, instanceID: %s",
		functionID, workerID, instanceID)

	updateInstanceState(workerID, functionID, instanceID, state.InstanceStateStarting)
	return workerID, instanceID, nil
}
