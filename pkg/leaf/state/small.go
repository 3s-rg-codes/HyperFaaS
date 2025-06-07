package state

// TODOs: find a way to update workers list when workers are added or removed.

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
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

func (s *SmallState) AddFunction(functionID FunctionID, metricChan chan bool, scaleUpCallback func(ctx context.Context, functionID FunctionID, workerID WorkerID) error) {
	s.mu.Lock()
	s.autoscalers[functionID] = NewAutoscaler(functionID, s.workers, metricChan, scaleUpCallback, s.logger)
	// TODO: pick a way to manage context correctly
	go s.autoscalers[functionID].Scale(context.TODO())
	s.mu.Unlock()
}

func (s *SmallState) GetAutoscaler(functionID FunctionID) *Autoscaler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoscalers[functionID]
}

type Autoscaler struct {
	functionID                FunctionID
	workers                   []WorkerID
	panicMode                 atomic.Bool
	MetricChan                chan bool
	concurrencyLevel          atomic.Int32
	runningInstances          atomic.Int32
	startingInstances         atomic.Int32
	maxStartingInstances      int32
	maxRunningInstances       int32
	targetInstanceConcurrency int32
	evaluationInterval        time.Duration
	scaleUpCallback           func(ctx context.Context, functionID FunctionID, workerID WorkerID) error
	logger                    *slog.Logger
}

func NewAutoscaler(functionID FunctionID, workers []WorkerID, metricChan chan bool, scaleUpCallback func(ctx context.Context, functionID FunctionID, workerID WorkerID) error, logger *slog.Logger) *Autoscaler {
	return &Autoscaler{
		functionID:                functionID,
		workers:                   workers,
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
			if concurrencyLevel/runningInstances > a.targetInstanceConcurrency {
				if runningInstances < a.maxRunningInstances && startingInstances < a.maxStartingInstances {
					a.startingInstances.Add(1)
					// TODO: pick a better way to pick a worker.
					err := a.scaleUpCallback(ctx, a.functionID, a.workers[0])
					if err != nil {
						a.logger.Error("Failed to scale up", "error", err)
					} else {
						// if scale up was successful, decrement starting instances and increment running instances
						a.startingInstances.Add(-1)
						a.runningInstances.Add(1)
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

func (a *Autoscaler) ForceScaleUp(ctx context.Context) error {
	if a.startingInstances.Load() >= a.maxStartingInstances {
		return &TooManyStartingInstancesError{FunctionID: a.functionID}
	}
	// turn on panic mode
	a.panicMode.Store(true)
	a.startingInstances.Add(1)
	// TODO: implement a better way to pick a worker.
	err := a.scaleUpCallback(ctx, a.functionID, a.workers[0])
	if err != nil {
		a.logger.Error("Failed to scale up", "error", err)
		return &ScaleUpFailedError{FunctionID: a.functionID, WorkerID: a.workers[0], Err: err}
	}
	// scale up was successful
	a.panicMode.Store(false)
	a.startingInstances.Add(-1)
	a.runningInstances.Add(1)
	return nil
}
