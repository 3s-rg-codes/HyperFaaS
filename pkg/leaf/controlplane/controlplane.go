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
				// update in-memory metrics to compute scaling decisions.
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

// UpsertFunction inserts or updates a function in the control plane.
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
		go scaler.AutoScale()
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

	scaleToZeroAfter := globalCfg.ScaleToZeroAfter
	if cfg != nil && cfg.GetTimeout() > 0 {
		scaleToZeroAfter = time.Duration(cfg.GetTimeout()) * time.Second
	}

	controller := &functionAutoScaler{
		functionID:                functionID,
		containerized:             globalCfg.Containerized,
		maxConcurrencyPerInstance: maxConcurrency,
		maxInstancesPerWorker:     globalCfg.MaxInstancesPerWorker,
		scaleToZeroAfter:          scaleToZeroAfter,
		startTimeout:              globalCfg.StartTimeout,
		stopTimeout:               globalCfg.StopTimeout,
		workers:                   workers,
		logger:                    logger.With("function_id", functionID, "component", "autoscaler"),
		scheduler:                 scheduler,
		ctx:                       subCtx,
		cancel:                    cancel,
		workerStates:              make([]workerState, len(workers)),
		instanceChangesChan:       instanceChangesChan,
		zeroScaleChan:             zeroScaleChan,
		concurrencyReporter:       concurrencyReporter,
	}

	for i := range controller.workerStates {
		controller.workerStates[i].instances = make([]instance, 0, globalCfg.MaxInstancesPerWorker)
	}

	return controller
}

// Close cancels the context of this function, stopping all operations and cleaning up.
func (f *functionAutoScaler) Close() {
	f.cancel()
}

func (f *functionAutoScaler) handleMetricEvent(concurrency int64) {
	f.inFlight.Store(concurrency)
}

func (f *functionAutoScaler) AutoScale() {
	t := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-t.C:
			f.reconcile()
		}
	}
}

// reconcile compares desired and actual instance counts and scales accordingly.
// for now, we only have a dumb scaling algo. once have multiple, we will want to add
// an interface to be able to DI different scaling algos.
func (f *functionAutoScaler) reconcile() {
	now := time.Now()

	f.mu.Lock()
	actual := f.totalInstances
	maxConcurrency := f.maxConcurrencyPerInstance
	scaleToZeroAfter := f.scaleToZeroAfter
	f.mu.Unlock()

	desired := f.desiredInstanceCount(now, actual, maxConcurrency, scaleToZeroAfter)
	if desired == actual {
		return
	}

	if desired > actual {
		f.scaleUpTo(desired - actual)
		return
	}

	f.scaleDownTo(actual - desired)
}

func (f *functionAutoScaler) desiredInstanceCount(now time.Time, actual int, maxConcurrency int, scaleToZeroAfter time.Duration) int {
	inFlight := f.inFlight.Load()
	if inFlight > 0 {
		// todo: get rid of this once we have proper validation
		if maxConcurrency <= 0 {
			maxConcurrency = 1
		}
		return int((inFlight + int64(maxConcurrency) - 1) / int64(maxConcurrency))
	}

	lastRequest := f.concurrencyReporter.LastRequestTimestamp(f.functionID)
	if lastRequest.IsZero() {
		return 0
	}
	if now.Sub(lastRequest) <= scaleToZeroAfter {
		return actual
	}
	return 0
}

func (f *functionAutoScaler) scaleUpTo(count int) {
	for range count {
		wIdx, ok := f.scheduler.PickForScale(f.snapshotWorkerState(), f.maxInstancesPerWorker)
		if !ok {
			f.logger.Warn("no worker available for scaling", "function_id", f.functionID)
			return
		}
		err := f.scaleUp(f.ctx, wIdx, "reconcile-scale-up")
		if err != nil {
			// TODO handle this error better.
			f.logger.Error("failed to scale up", "function_id", f.functionID, "error", err)
			return
		}
	}
}

// scaleDownTo stops and removes the specified number of instances.
// TODO: change this, stop in parallel
func (f *functionAutoScaler) scaleDownTo(count int) {
	stopList := f.instancesForStop(count)
	for _, item := range stopList {
		ctx, cancel := context.WithTimeout(f.ctx, f.stopTimeout)
		_, err := f.workers[item.workerIdx].Stop(ctx, &workerpb.StopRequest{InstanceId: item.instanceID})
		cancel()
		if err != nil {
			f.logger.Warn("failed to stop instance", "worker", f.workers[item.workerIdx].Address(), "instance", item.instanceID, "error", err)
			// TODO: add retries or backoff for failed stop attempts.
			continue
		}
		f.removeInstance(item.workerIdx, item.instanceID)
		f.logger.Info("stopped instance", "worker", f.workers[item.workerIdx].Address(), "instance_id", item.instanceID, "reason", "reconcile")
	}
}

// scaleUp sends a Start call to a worker and updates local state accordingly.
func (f *functionAutoScaler) scaleUp(ctx context.Context, workerIdx int, reason string) error {
	f.logger.Debug(" SCALEUP attempting to scale up", "function_id", f.functionID, "worker_idx", workerIdx, "reason", reason)
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
		f.logger.Error(" SCALEUP failed to send start request", "function_id", f.functionID, "worker_idx", workerIdx, "reason", reason, "error", err)
		return err
	}
	f.logger.Info("started instance", "worker", worker.Address(), "instance_id", resp.InstanceId)

	// Select the appropriate IP based on whether we're containerized
	instanceIP := resp.InstanceInternalIp
	if !f.containerized {
		instanceIP = resp.InstanceExternalIp
	}

	var emitZeroScale bool

	f.mu.Lock()
	ws := &f.workerStates[workerIdx]
	ws.instances = append(ws.instances, instance{
		id:        resp.InstanceId,
		ip:        instanceIP,
		lastUsage: time.Now(),
	})
	f.totalInstances++
	emitZeroScale = f.totalInstances == 1
	f.mu.Unlock()

	f.logger.Debug(" SCALEUP emitting instance change", "function_id", f.functionID, "address", instanceIP)
	f.instanceChangesChan <- metrics.InstanceChange{
		FunctionId: f.functionID,
		Address:    instanceIP,
		Have:       true,
	}
	f.logger.Debug(" SCALEUP emitted instance change successfully", "function_id", f.functionID, "address", instanceIP)

	if emitZeroScale {
		f.logger.Debug(" SCALEUP emitting zero scale event", "function_id", f.functionID, "zero", true)
		f.zeroScaleChan <- metrics.ZeroScaleEvent{
			FunctionId: f.functionID,
			Have:       true,
		}
		f.logger.Debug(" SCALEUP emitted zero scale event successfully", "function_id", f.functionID, "zero", true)
	}

	return nil
}

type stopRequest struct {
	workerIdx  int
	instanceID string
}

func (f *functionAutoScaler) instancesForStop(count int) []stopRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	stopList := make([]stopRequest, 0, count)
	for idx := range f.workerStates {
		ws := &f.workerStates[idx]
		for _, inst := range ws.instances {
			stopList = append(stopList, stopRequest{workerIdx: idx, instanceID: inst.id})
			if len(stopList) >= count {
				return stopList
			}
		}
	}
	return stopList
}

// removeInstance removes an instance from local state.
func (f *functionAutoScaler) removeInstance(workerIdx int, instanceID string) {
	if workerIdx < 0 || workerIdx >= len(f.workerStates) {
		return
	}

	var (
		instanceIP   string
		scaledToZero bool
	)

	f.mu.Lock()
	ws := &f.workerStates[workerIdx]
	for i := range ws.instances {
		if ws.instances[i].id == instanceID {
			instanceIP = ws.instances[i].ip
			last := len(ws.instances) - 1
			ws.instances[i] = ws.instances[last]
			ws.instances = ws.instances[:last]
			f.totalInstances--
			scaledToZero = f.totalInstances == 0
			break
		}
	}
	f.mu.Unlock()

	if instanceIP == "" {
		return
	}

	f.instanceChangesChan <- metrics.InstanceChange{
		FunctionId: f.functionID,
		Address:    instanceIP,
		Have:       false,
	}

	if scaledToZero {
		f.logger.Debug("emitting zero scale event", "function_id", f.functionID, "zero", false)
		f.zeroScaleChan <- metrics.ZeroScaleEvent{
			FunctionId: f.functionID,
			Have:       false,
		}
		f.concurrencyReporter.DeleteFunctionStats(f.functionID)
	}
}

// handleWorkerEvent reconciles local state against incoming workerStatusEvents
func (f *functionAutoScaler) handleWorkerEvent(workerIdx int, event *dataplane.WorkerStatusEvent) {
	if event == nil {
		return
	}
	switch event.Event {
	case workerpb.Event_EVENT_DOWN, workerpb.Event_EVENT_TIMEOUT:
		f.logger.Info("removing instance", "function_id", f.functionID, "worker", workerIdx, "instance_id", event.InstanceId, "reason", event.Event)
		f.removeInstance(workerIdx, event.InstanceId)
	}
}

var errNoCapacity = errors.New("no spare capacity available")

// snapshotWorkerState returns a copy of the current worker states.
func (f *functionAutoScaler) snapshotWorkerState() []WorkerState {
	f.mu.Lock()
	defer f.mu.Unlock()

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

	if cfg.GetTimeout() > 0 {
		f.scaleToZeroAfter = time.Duration(cfg.GetTimeout()) * time.Second
	}
}
