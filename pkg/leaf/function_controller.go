package leafv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
)

// functionController is responsible for scaling and routing calls to the workers.
type functionController struct {
	functionID string

	maxConcurrencyPerInstance int
	maxInstancesPerWorker     int
	scaleToZeroAfter          time.Duration
	startTimeout              time.Duration
	stopTimeout               time.Duration

	workers   []*workerClient
	logger    *slog.Logger
	scheduler WorkerScheduler
	// the context of this function controller. used to cancel/destroy all operations.
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex

	workerStates []workerState
	waiters      []chan struct{}

	scaling bool

	totalInFlight  int
	totalInstances int
	lastRequest    time.Time
	// how often the idle ticker should check for idle instances and attempt to scale them down
	idleTickerInterval time.Duration
}

// local representation of a single worker holding instances for this function.
type workerState struct {
	instances []instance
	// how many requests are currently being processed.
	inFlight int
}

// local representation of a single instance on a worker.
type instance struct {
	lastUsage time.Time
	id        string
	ip        string
}

func newFunctionController(ctx context.Context, functionID string, cfg *common.Config, workers []*workerClient, globalCfg Config, logger *slog.Logger) *functionController {
	subCtx, cancel := context.WithCancel(ctx)

	maxConcurrency := int(cfg.GetMaxConcurrency())
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	scheduler := globalCfg.SchedulerFactory(functionID, len(workers), logger)

	controller := &functionController{
		functionID:                functionID,
		maxConcurrencyPerInstance: maxConcurrency,
		maxInstancesPerWorker:     globalCfg.MaxInstancesPerWorker,
		scaleToZeroAfter:          globalCfg.ScaleToZeroAfter,
		startTimeout:              globalCfg.StartTimeout,
		stopTimeout:               globalCfg.StopTimeout,
		workers:                   workers,
		logger:                    logger,
		scheduler:                 scheduler,
		ctx:                       subCtx,
		cancel:                    cancel,
		workerStates:              make([]workerState, len(workers)),
		waiters:                   make([]chan struct{}, 0, 32),
		lastRequest:               time.Now(),
		// seems like a good initial value, so we dont check too often.
		idleTickerInterval: globalCfg.ScaleToZeroAfter / 2,
	}

	for i := range controller.workerStates {
		controller.workerStates[i].instances = make([]instance, 0, globalCfg.MaxInstancesPerWorker)
	}

	go controller.killIdleInstances()

	return controller
}

// stop cancels the context of the function controller.
func (f *functionController) stop() {
	f.cancel()
}

func (f *functionController) updateConfig(cfg *common.Config) {
	if cfg == nil {
		return
	}
	maxConcurrency := int(cfg.GetMaxConcurrency())

	f.mu.Lock()
	f.maxConcurrencyPerInstance = maxConcurrency
	f.mu.Unlock()
}

// routeCall forwards a call to a worker and tags its instances as used. It finds the worker by using the workerScheduler.
func (f *functionController) routeCall(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {
	workerIdx, release, err := f.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	worker := f.workers[workerIdx]
	resp, callErr := worker.Call(ctx, req)
	if callErr != nil {
		f.logger.Error("worker call failed", "worker", worker.Address(), "error", callErr)
		// maybe scale up depending on the error?
		f.handleCallError(workerIdx, callErr)
		return nil, callErr
	}

	f.markInstanceUsed(workerIdx)

	return resp, nil
}

func (f *functionController) acquireSlot(ctx context.Context) (int, func(), error) {
	for {
		f.mu.Lock()
		f.lastRequest = time.Now()

		idx := f.pickWorkerForCall()

		if idx >= 0 { // we found a worker
			ws := &f.workerStates[idx]
			// update state
			ws.inFlight++
			f.totalInFlight++
			release := f.makeRelease(idx)
			f.mu.Unlock()
			return idx, release, nil
		}
		// we didnt find a worker
		if !f.scaling {
			target, ok := f.pickWorkerForScale()
			if ok { // found a worker to scale up
				f.scaling = true
				// release lock while we are waiting for the worker to scale up
				f.mu.Unlock()

				scaleErr := f.scaleUp(ctx, target)

				f.mu.Lock()
				f.scaling = false
				if scaleErr != nil {
					f.mu.Unlock()
					if errors.Is(scaleErr, errNoCapacity) {
						// try to acquire slot again
						continue
					}
					return -1, nil, scaleErr
				}
				f.notifyWaiters()
				f.mu.Unlock()
				// we scaled up, try to pick again
				continue
			}
		}

		// we are scaling, wait in a queue so we dont scale up to infinity

		waiter := f.enqueueWaiter()
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			f.removeWaiter(waiter)
			return -1, nil, ctx.Err()
		case <-waiter:
			continue
		}
	}
}

// pickWorkerForCall uses the scheduler to pick a worker for a call.
// ONly call this while holding the lock.
func (f *functionController) pickWorkerForCall() int {
	if f.scheduler == nil {
		return -1
	}
	return f.scheduler.PickForCall(f.snapshotWorkerState(), f.maxConcurrencyPerInstance)
}

// pickWorkerForScale uses the scheduler to pick a worker for scaling up.
// ONly call this while holding the lock.
func (f *functionController) pickWorkerForScale() (int, bool) {
	if f.scheduler == nil {
		return -1, false
	}
	return f.scheduler.PickForScale(f.snapshotWorkerState(), f.maxInstancesPerWorker)
}

// scaleUp sends a Start call to a worker and updates local state accordingly.
func (f *functionController) scaleUp(ctx context.Context, workerIdx int) error {
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

	f.mu.Lock()
	defer f.mu.Unlock()
	// then update local state
	ws := &f.workerStates[workerIdx]
	ws.instances = append(ws.instances, instance{id: resp.InstanceId, ip: resp.InstanceIp, lastUsage: time.Now()})
	f.totalInstances++
	f.notifyWaiters()

	f.logger.Debug("started instance", "worker", worker.Address(), "instance", resp.InstanceId)

	return nil
}

// enqueueWaiter adds a waiter channel to the waiters list and returns it.
// Only call if holding the lock.
func (f *functionController) enqueueWaiter() chan struct{} {
	ch := make(chan struct{}, 1)
	f.waiters = append(f.waiters, ch)
	return ch
}

// notifyWaiters sends a singal to all waiters in the list.
// Only call if holding the lock.
func (f *functionController) notifyWaiters() {
	for len(f.waiters) > 0 {
		ch := f.waiters[0]
		f.waiters = f.waiters[1:]
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// removeWaiter removes a waiter from the list
func (f *functionController) removeWaiter(target chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, ch := range f.waiters {
		if ch == target {
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			return
		}
	}
}

// makeRelease returns a function that reduces the total inFlight request count
// and of a specific worker.
func (f *functionController) makeRelease(workerIdx int) func() {
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		ws := &f.workerStates[workerIdx]
		if ws.inFlight > 0 {
			ws.inFlight--
			f.totalInFlight--
		}
		f.notifyWaiters()
	}
}

// handleCallError implements what happens when a call fails.
// TODO: improve this with different strategies once we have resource-based scaling.
func (f *functionController) handleCallError(workerIdx int, err error) {
	st, ok := status.FromError(err)
	if !ok {
		return
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		go f.ensureCapacity(workerIdx)
	}
}

// ensureCapacity ensures that the worker has enough running instances to handle the current load.
func (f *functionController) ensureCapacity(workerIdx int) {
	ctx, cancel := context.WithTimeout(f.ctx, f.startTimeout)
	defer cancel()

	f.mu.Lock()
	ws := f.workerStates[workerIdx]
	capacity := len(ws.instances) * f.maxConcurrencyPerInstance
	inFlight := ws.inFlight
	f.mu.Unlock()

	if capacity > 0 && inFlight < capacity {
		return
	}
	// TODO: handle error
	_ = f.scaleUp(ctx, workerIdx)
}

// markInstanceUsed resets the lastUsage of all instances in a worker to time.Now().
// todo: include call metadata in CallResponse and use instance_id to correctly tag only the specific instance
// that answered.
func (f *functionController) markInstanceUsed(workerIdx int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ws := &f.workerStates[workerIdx]
	now := time.Now()
	for i := range ws.instances {
		ws.instances[i].lastUsage = now
	}
}

// killIdleInstances tries to scale to zero every idleTicker interval.
func (f *functionController) killIdleInstances() {
	t := time.NewTicker(f.idleTickerInterval)
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-t.C:
			f.tryScaleToZero()
		}
	}
}

// tryScaleToZero scales to zero if the function has been idle for too long.
func (f *functionController) tryScaleToZero() {
	f.mu.Lock()
	if f.totalInFlight > 0 || f.totalInstances == 0 {
		f.mu.Unlock()
		return
	}
	idleFor := time.Since(f.lastRequest)
	if idleFor < f.scaleToZeroAfter {
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
	}
}

type stopRequest struct {
	workerIdx  int
	instanceID string
}

// removeInstance removes an instance from local state.
func (f *functionController) removeInstance(workerIdx int, instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if workerIdx < 0 || workerIdx >= len(f.workerStates) {
		return
	}
	ws := &f.workerStates[workerIdx]
	for i := range ws.instances {
		if ws.instances[i].id == instanceID {
			last := len(ws.instances) - 1
			ws.instances[i] = ws.instances[last]
			ws.instances = ws.instances[:last]
			f.totalInstances--
			f.notifyWaiters()
			return
		}
	}
}

// handleWorkerEvent reconciles local state against incoming workerStatusEvents
func (f *functionController) handleWorkerEvent(workerIdx int, event *workerStatusEvent) {
	if event == nil {
		return
	}
	switch event.event {
	// for now all of this is the same ...
	case workerpb.Event_EVENT_DOWN, workerpb.Event_EVENT_TIMEOUT, workerpb.Event_EVENT_STOP:
		f.removeInstance(workerIdx, event.instanceID)
	}
}

var errNoCapacity = errors.New("no spare capacity available")

// snapshotWorkerState returns a copy of the current worker states.
func (f *functionController) snapshotWorkerState() []WorkerState {
	snapshots := make([]WorkerState, len(f.workerStates))
	for i := range f.workerStates {
		ws := &f.workerStates[i]
		snapshots[i] = WorkerState{
			Index:     i,
			Instances: len(ws.instances),
			InFlight:  ws.inFlight,
		}
	}
	return snapshots
}
