package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
)

// The maximum queue size for the zero scale events channel.
// This channel is constantly read by the State stream to know if this leaf manages instances for a function. In theory, it should never be full.
const ZERO_SCALE_CHANNEL_BUFFER = 1000

// ControlPlane is responsible for scaling and managing instances for multiple functions.
type ControlPlane struct {
	ctx context.Context
	cfg config.Config

	logger    *slog.Logger
	mu        sync.RWMutex
	functions map[string]*functionAutoScaler

	workers []*dataplane.WorkerClient

	// the concurrency reporter to read metrics from.
	// Scale from zero is triggered by receiving a MetricEvent with ColdStart set to true.
	concurrencyReporter *metrics.ConcurrencyReporter

	// instance scaling events get reported into this channel.
	instanceChangesChan chan metrics.InstanceChange

	// used to publish zero scale events to the State stream.
	functionScaleEvents chan metrics.ZeroScaleEvent
}

func NewControlPlane(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	instanceChangesChan chan metrics.InstanceChange,
	workers []*dataplane.WorkerClient,
	functionScaleEvents chan metrics.ZeroScaleEvent,
	concurrencyReporter *metrics.ConcurrencyReporter,
) *ControlPlane {
	return &ControlPlane{
		ctx:                 ctx,
		cfg:                 cfg,
		logger:              logger.With("component", "controlplane"),
		functions:           make(map[string]*functionAutoScaler),
		concurrencyReporter: concurrencyReporter,
		instanceChangesChan: instanceChangesChan,
		workers:             workers,
		functionScaleEvents: functionScaleEvents,
	}
}

// Run makes the control plane react to incoming metric events.
func (c *ControlPlane) Run(ctx context.Context) {
	mc := c.concurrencyReporter.GetMetricChan()
	for {
		select {
		case <-ctx.Done():
			return
		case metricEvent := <-mc:

			go func(ev metrics.MetricEvent) {
				c.mu.RLock()
				scaler, ok := c.functions[ev.FunctionId]
				c.mu.RUnlock()

				if !ok {
					c.logger.Error("tried to process event for unknown function", "function_id", ev.FunctionId)
					return
				}
				if ev.ColdStart {
					c.logger.Debug("received cold start metric event", "function_id", ev.FunctionId)
					wIdx, ok := scaler.scheduler.PickForScale(scaler.snapshotWorkerState(), scaler.maxInstancesPerWorker)
					if !ok {
						c.logger.Warn("no worker available for scaling", "function_id", ev.FunctionId)
						return
					}
					err := scaler.scaleUp(ctx, wIdx, "cold-start")
					if err != nil {
						// TODO do something better about this error.
						c.logger.Error("failed to scale up", "function_id", ev.FunctionId, "error", err)
						return
					}
				}
				// not a cold start, just update in memory metrics to compute scaling decisions.
				scaler.handleMetricEvent(ev.Concurrency)
			}(metricEvent)
		}
	}
}

// FunctionExists checks if a function is registered in the control plane.
func (c *ControlPlane) FunctionExists(functionId string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.functions[functionId]
	return ok
}

// RemoveFunction removes a function from the control plane state, calling Close() on it first.
func (c *ControlPlane) RemoveFunction(functionId string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	scaler, ok := c.functions[functionId]
	if !ok {
		return
	}
	c.concurrencyReporter.DeleteFunctionStats(functionId)
	scaler.Close()
	delete(c.functions, functionId)

}

func (c *ControlPlane) HandleWorkerEvent(workerIdx int, event *dataplane.WorkerStatusEvent) {
	c.mu.RLock()
	scaler, ok := c.functions[event.FunctionId]
	c.mu.RUnlock()
	if !ok {
		return
	}
	scaler.handleWorkerEvent(workerIdx, event)
}

// GetScaleChan returns the channel to read zero scale events for a function from.
func (c *ControlPlane) GetScaleChan(functionId string) chan metrics.ZeroScaleEvent {
	c.mu.RLock()
	scaler, ok := c.functions[functionId]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return scaler.zeroScaleChan
}

func (c *ControlPlane) UpsertFunction(meta *metadata.FunctionMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	scaler, ok := c.functions[meta.ID]
	if !ok {
		scaler = newFunctionAutoScaler(c.ctx,
			meta.ID,
			meta.Config,
			c.instanceChangesChan,
			c.workers,
			c.cfg,
			c.logger,
			c.functionScaleEvents,
			c.concurrencyReporter,
		)
		c.functions[meta.ID] = scaler
	}
	scaler.updateConfig(meta.Config)
}

// functionAutoScaler is responsible for scaling and managing instances.
// It's the control plane component that manages worker states and instance lifecycle.
type functionAutoScaler struct {
	functionID string

	maxConcurrencyPerInstance int
	maxInstancesPerWorker     int
	scaleToZeroAfter          time.Duration
	startTimeout              time.Duration
	stopTimeout               time.Duration

	// how many requests are currently in flight.
	inFlight atomic.Int64

	// when the last request was received.
	lastRequestTimestamp time.Time

	// whether the leaf is running in a containerized environment.
	containerized bool

	// used to start and stop instances.
	workers []*dataplane.WorkerClient

	logger    *slog.Logger
	scheduler WorkerScheduler
	// the context of this function controller. used to cancel/destroy all operations.
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex

	workerStates []workerState

	instanceChangesChan chan metrics.InstanceChange

	totalInstances int
	// how often the idle ticker should check for idle instances and attempt to scale them down
	idleTickerInterval time.Duration

	// function scale FROM and TO zero get reported into this channel.
	zeroScaleChan chan metrics.ZeroScaleEvent

	concurrencyReporter *metrics.ConcurrencyReporter
}

// local representation of a single worker holding instances for this function.
type workerState struct {
	instances []instance
}

// local representation of a single instance on a worker.
type instance struct {
	lastUsage time.Time
	id        string
	ip        string
}

func newFunctionAutoScaler(ctx context.Context,
	functionID string,
	cfg *common.Config,
	instanceChangesChan chan metrics.InstanceChange,
	workers []*dataplane.WorkerClient,
	globalCfg config.Config,
	logger *slog.Logger,
	zeroScaleChan chan metrics.ZeroScaleEvent,
	concurrencyReporter *metrics.ConcurrencyReporter,
) *functionAutoScaler {
	// create a new context for this function, that way, we can cancel all operations for this function when the function is deleted.
	subCtx, cancel := context.WithCancel(ctx)

	maxConcurrency := int(cfg.GetMaxConcurrency())
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	scheduler := newBalancedRoundRobin(len(workers))

	controller := &functionAutoScaler{
		functionID:                functionID,
		containerized:             globalCfg.Containerized,
		maxConcurrencyPerInstance: maxConcurrency,
		maxInstancesPerWorker:     globalCfg.MaxInstancesPerWorker,
		scaleToZeroAfter:          globalCfg.ScaleToZeroAfter,
		startTimeout:              globalCfg.StartTimeout,
		stopTimeout:               globalCfg.StopTimeout,
		workers:                   workers,
		logger:                    logger.With("function_id", functionID, "component", "autoscaler"),
		scheduler:                 scheduler,
		ctx:                       subCtx,
		cancel:                    cancel,
		workerStates:              make([]workerState, len(workers)),
		idleTickerInterval:        globalCfg.ScaleToZeroAfter / 2,
		instanceChangesChan:       instanceChangesChan,
		zeroScaleChan:             zeroScaleChan,
		concurrencyReporter:       concurrencyReporter,
	}

	for i := range controller.workerStates {
		controller.workerStates[i].instances = make([]instance, 0, globalCfg.MaxInstancesPerWorker)
	}

	go controller.AutoScale()

	return controller
}

// Close cancels the context of this function, stopping all operations and cleaning up.
func (f *functionAutoScaler) Close() {
	f.cancel()
}

func (f *functionAutoScaler) handleMetricEvent(concurrency int64) {
	// maybe consistency is not needed here so we just dont lock?
	if concurrency > 0 {
		f.lastRequestTimestamp = time.Now()
	}

	f.inFlight.Store(concurrency)
}

func (f *functionAutoScaler) AutoScale() {
	t := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-t.C:
			if time.Since(f.lastRequestTimestamp) > f.scaleToZeroAfter &&
				f.totalInstances > 0 &&
				f.inFlight.Load() == 0 {
				f.logger.Info("scaling to zero",
					"function_id", f.functionID,
					"total_instances", f.totalInstances,
					// should be 0 but for debugging purposes.
					"current_load", f.inFlight.Load(),
				)
				f.tryScaleToZero()
			}
			l := f.inFlight.Load()
			is := f.totalInstances

			if l == 0 {
				continue
			}
			// load is greater than 0, but no instances are running.
			if is == 0 {
				wIdx, ok := f.scheduler.PickForScale(f.snapshotWorkerState(), f.maxInstancesPerWorker)
				if !ok {
					f.logger.Warn("no worker available for scaling", "function_id", f.functionID)
					continue
				}
				err := f.scaleUp(f.ctx, wIdx, "non-zero-load-no-instances")
				if err != nil {
					// TODO remove after debugging
					panic(err)
				}
				continue
			}
			// load is greater than the number of instances times the max concurrency per instance.
			if l > int64(is*f.maxConcurrencyPerInstance) {
				wIdx, ok := f.scheduler.PickForScale(f.snapshotWorkerState(), f.maxInstancesPerWorker)
				if !ok {
					f.logger.Warn("no worker available for scaling", "function_id", f.functionID)
					continue
				}
				err := f.scaleUp(f.ctx, wIdx, "load-greater-than-instances-times-max-concurrency")
				if err != nil {
					// TODO remove after debugging
					panic(err)
				}
				continue
			}

			// TODO: implement scaling down.
		}
	}
}

// scaleUp sends a Start call to a worker and updates local state accordingly.
func (f *functionAutoScaler) scaleUp(ctx context.Context, workerIdx int, reason string) error {
	if workerIdx < 0 || workerIdx >= len(f.workers) {
		return fmt.Errorf("invalid worker index %d", workerIdx)
	}
	// first check capacity
	f.mu.Lock()
	if len(f.workerStates[workerIdx].instances) >= f.maxInstancesPerWorker {
		f.mu.Unlock()
		return errNoCapacity
	}
	f.mu.Unlock()

	worker := f.workers[workerIdx]

	subCtx, cancel := context.WithTimeout(ctx, f.startTimeout)
	defer cancel()

	resp, err := worker.Start(subCtx, &workerpb.StartRequest{FunctionId: f.functionID})
	if err != nil {
		return err
	}
	f.logger.Info("started instance", "worker", worker.Address(), "instance_id", resp.InstanceId, "reason", reason)

	f.mu.Lock()
	defer f.mu.Unlock()

	// Select the appropriate IP based on whether we're containerized
	instanceIP := resp.InstanceInternalIp
	if !f.containerized {
		instanceIP = resp.InstanceExternalIp
	}

	// emit instance change
	f.logger.Debug("emitting instance change", "function_id", f.functionID, "address", instanceIP)
	f.instanceChangesChan <- metrics.InstanceChange{
		FunctionId: f.functionID,
		Address:    instanceIP,
		Have:       true,
	}

	// track instance in local state
	ws := &f.workerStates[workerIdx]
	ws.instances = append(ws.instances, instance{
		id:        resp.InstanceId,
		ip:        instanceIP,
		lastUsage: time.Now(),
	})
	f.totalInstances++

	if f.totalInstances == 1 {
		f.logger.Debug("emitting zero scale event", "function_id", f.functionID, "zero", true)
		f.zeroScaleChan <- metrics.ZeroScaleEvent{
			FunctionId: f.functionID,
			Have:       true,
		}
	}

	return nil
}

// tryScaleToZero scales to zero if the function has been idle for too long.
func (f *functionAutoScaler) tryScaleToZero() {
	f.mu.Lock()
	if f.totalInstances == 0 {
		f.mu.Unlock()
		return
	}

	stopList := make([]stopRequest, 0, f.totalInstances)
	for idx := range f.workerStates {
		ws := &f.workerStates[idx]
		for _, inst := range ws.instances {
			stopList = append(stopList, stopRequest{workerIdx: idx, instanceID: inst.id})
		}
	}
	f.mu.Unlock()

	for _, item := range stopList {
		ctx, cancel := context.WithTimeout(f.ctx, f.stopTimeout)
		_, err := f.workers[item.workerIdx].Stop(ctx, &workerpb.StopRequest{InstanceId: item.instanceID})
		cancel()
		if err != nil {
			f.logger.Warn("failed to stop instance", "worker", f.workers[item.workerIdx].Address(), "instance", item.instanceID, "error", err)
			continue
		}
		f.removeInstance(item.workerIdx, item.instanceID)
		f.logger.Info("stopped instance", "worker", f.workers[item.workerIdx].Address(), "instance_id", item.instanceID, "reason", "idle", "totalInstances", f.totalInstances)
	}

	if f.totalInstances == 0 {
		f.logger.Debug("emitting zero scale event", "function_id", f.functionID, "zero", false)
		f.zeroScaleChan <- metrics.ZeroScaleEvent{
			FunctionId: f.functionID,
			Have:       false,
		}

		f.concurrencyReporter.DeleteFunctionStats(f.functionID)
	}
}

type stopRequest struct {
	workerIdx  int
	instanceID string
}

// removeInstance removes an instance from local state.
func (f *functionAutoScaler) removeInstance(workerIdx int, instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if workerIdx < 0 || workerIdx >= len(f.workerStates) {
		return
	}

	ws := &f.workerStates[workerIdx]
	for i := range ws.instances {
		if ws.instances[i].id == instanceID {
			// save the IP before swapping
			instanceIP := ws.instances[i].ip
			last := len(ws.instances) - 1
			ws.instances[i] = ws.instances[last]
			ws.instances = ws.instances[:last]

			// emit instance change
			f.instanceChangesChan <- metrics.InstanceChange{
				FunctionId: f.functionID,
				Address:    instanceIP,
				Have:       false,
			}

			f.totalInstances--

			return
		}
	}
}

// handleWorkerEvent reconciles local state against incoming workerStatusEvents
func (f *functionAutoScaler) handleWorkerEvent(workerIdx int, event *dataplane.WorkerStatusEvent) {
	if event == nil {
		return
	}
	switch event.Event {
	// for now all of this is the same ...
	case workerpb.Event_EVENT_DOWN, workerpb.Event_EVENT_TIMEOUT, workerpb.Event_EVENT_STOP:
		f.logger.Info("removing instance", "function_id", f.functionID, "worker", workerIdx, "instance_id", event.InstanceId, "reason", event.Event)
		f.removeInstance(workerIdx, event.InstanceId)
	}
}

var errNoCapacity = errors.New("no spare capacity available")

// snapshotWorkerState returns a copy of the current worker states.
func (f *functionAutoScaler) snapshotWorkerState() []WorkerState {
	snapshots := make([]WorkerState, len(f.workerStates))
	for i := range f.workerStates {
		ws := &f.workerStates[i]
		snapshots[i] = WorkerState{
			Index:     i,
			Instances: len(ws.instances),
			InFlight:  0, // We don't track in-flight in control plane anymore. todo remove
		}
	}
	return snapshots
}

func (f *functionAutoScaler) updateConfig(cfg *common.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxConcurrencyPerInstance = int(cfg.GetMaxConcurrency())
	if f.maxConcurrencyPerInstance <= 0 {
		f.maxConcurrencyPerInstance = 1
	}
}
