package state

// TODOs: find a way to update workers list when workers are added or removed.

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type (
	WorkerID   string
	InstanceID string
	FunctionID string
)

type SmallState struct {
	mu          sync.RWMutex
	workers     []WorkerID
	autoscalers map[FunctionID]*Autoscaler
	logger      *slog.Logger
}

func NewSmallState(workers []WorkerID, logger *slog.Logger) *SmallState {
	return &SmallState{
		workers:     workers,
		autoscalers: make(map[FunctionID]*Autoscaler),
		logger:      logger,
	}
}

func (s *SmallState) AddAutoscaler(functionID FunctionID, metricChan chan bool, scaleUpCallback func(ctx context.Context, functionID FunctionID, workerID WorkerID) error) {
	s.mu.Lock()
	s.autoscalers[functionID] = NewAutoscaler(functionID, s.workers, metricChan, scaleUpCallback, s.logger)
	// TODO: pick a way to manage context correctly
	go s.autoscalers[functionID].Scale(context.TODO())
	s.mu.Unlock()
}

func (s *SmallState) GetAutoscaler(functionID FunctionID) (*Autoscaler, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	autoscaler, ok := s.autoscalers[functionID]
	return autoscaler, ok
}

type Autoscaler struct {
	functionID FunctionID
	workers    []WorkerID

	// which workers are currently hosting instances for this function
	WorkersWithInstances      map[WorkerID]*atomic.Int32
	WorkersWithInstancesMutex sync.RWMutex

	panicMode                 atomic.Bool
	MetricChan                chan bool
	concurrencyLevel          atomic.Int32
	runningInstances          atomic.Int32
	startingInstances         atomic.Int32
	maxStartingInstances      int32
	maxRunningInstances       int32
	targetInstanceConcurrency int32
	// how often to evaluate the scaling algorithm
	evaluationInterval time.Duration
	// the function to be called when scaling up
	scaleUpCallback func(ctx context.Context, functionID FunctionID, workerID WorkerID) error
	logger          *slog.Logger
}

func NewAutoscaler(functionID FunctionID, workers []WorkerID, metricChan chan bool, scaleUpCallback func(ctx context.Context, functionID FunctionID, workerID WorkerID) error, logger *slog.Logger) *Autoscaler {
	return &Autoscaler{
		functionID:                functionID,
		workers:                   workers,
		WorkersWithInstances:      make(map[WorkerID]*atomic.Int32),
		MetricChan:                metricChan,
		scaleUpCallback:           scaleUpCallback,
		concurrencyLevel:          atomic.Int32{},
		runningInstances:          atomic.Int32{},
		startingInstances:         atomic.Int32{},
		logger:                    logger,
		evaluationInterval:        2 * time.Second,
		maxStartingInstances:      10,
		maxRunningInstances:       10,
		targetInstanceConcurrency: 250,
	}
}

func (a *Autoscaler) runMetricReceiver(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-a.MetricChan:
			if m {
				a.concurrencyLevel.Add(1)
			} else {
				a.concurrencyLevel.Add(-1)
			}
		}
	}
}

// Scale runs a simple scaling algorithm based on the concurrency level and the number of running instances across all workers.
// It currently just picks a random worker to scale up instances on.
// It does not implement any resource-based load balancing or scale-down logic.
func (a *Autoscaler) Scale(ctx context.Context) {
	go a.runMetricReceiver(ctx)
	ticker := time.NewTicker(a.evaluationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// run simple scaling algorithm
			concurrencyLevel := a.concurrencyLevel.Load()
			runningInstances := a.runningInstances.Load()
			startingInstances := a.startingInstances.Load()
			if concurrencyLevel == 0 {
				continue
			}
			if runningInstances == 0 || concurrencyLevel/runningInstances > a.targetInstanceConcurrency {
				if runningInstances < a.maxRunningInstances && startingInstances < a.maxStartingInstances {
					a.startingInstances.Add(1)

					target := a.pickRandomWorker()

					err := a.scaleUpCallback(ctx, a.functionID, target)
					if err != nil {
						a.logger.Error("Failed to scale up", "error", err)
					} else {
						// if scale up was successful, decrement starting instances and increment running instances
						a.startingInstances.Add(-1)
						a.runningInstances.Add(1)
						a.MarkInstanceStarted(target)
					}
				}
			}
		}
	}
}

func (a *Autoscaler) UpdateRunningInstances(delta int32) {
	a.runningInstances.Add(delta)
}

func (a *Autoscaler) IsScaledDown() bool {
	return a.runningInstances.Load() == 0
}

func (a *Autoscaler) IsPanicMode() bool {
	return a.panicMode.Load()
}

func (a *Autoscaler) ForceScaleUp(ctx context.Context) (WorkerID, error) {
	if a.startingInstances.Load() >= a.maxStartingInstances {
		return "", &TooManyStartingInstancesError{FunctionID: a.functionID}
	}
	// turn on panic mode
	a.panicMode.Store(true)
	a.startingInstances.Add(1)
	target := a.pickRandomWorker()
	err := a.scaleUpCallback(ctx, a.functionID, target)
	if err != nil {
		a.logger.Error("Failed to scale up", "error", err)
		return "", &ScaleUpFailedError{FunctionID: a.functionID, WorkerID: target, Err: err}
	}
	// scale up was successful
	a.panicMode.Store(false)
	a.startingInstances.Add(-1)
	a.runningInstances.Add(1)
	a.MarkInstanceStarted(target)
	return target, nil
}

// MarkInstanceStarted notes that the given worker is hosting at least one instance for this function
func (a *Autoscaler) MarkInstanceStarted(workerID WorkerID) {
	a.WorkersWithInstancesMutex.Lock()
	if a.WorkersWithInstances[workerID] == nil {
		a.WorkersWithInstances[workerID] = &atomic.Int32{}
	}
	a.WorkersWithInstances[workerID].Add(1)
	a.WorkersWithInstancesMutex.Unlock()
}

// MarkInstanceStopped notes that a worker is hosting one less instance for this function.
func (a *Autoscaler) MarkInstanceStopped(workerID WorkerID) {
	a.WorkersWithInstancesMutex.Lock()
	// counter must never be nil in this case
	if a.WorkersWithInstances[workerID] == nil {
		panic("tried to mark instance stopped for worker " + workerID + " but counter is nil")
	}
	a.WorkersWithInstances[workerID].Add(-1)
	a.WorkersWithInstancesMutex.Unlock()
}

// Pick selects a worker that currently hosts an instance if possible, otherwise falls back to the first worker.
func (a *Autoscaler) Pick() WorkerID {
	a.WorkersWithInstancesMutex.RLock()
	for w, c := range a.WorkersWithInstances {
		if c.Load() > 0 {
			a.WorkersWithInstancesMutex.RUnlock()
			return w
		}
	}
	a.WorkersWithInstancesMutex.RUnlock()
	if len(a.workers) > 0 {
		return a.workers[0]
	}
	return ""
}

// pick a random worker to scale up instances on
// TODO: implement resource-based load balancing here.
func (a *Autoscaler) pickRandomWorker() WorkerID {
	return a.workers[rand.Intn(len(a.workers))]
}
