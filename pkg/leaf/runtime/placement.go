package runtime

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf/autoscaling"
)

type balancedFastPath interface {
	NextWorkerIndex(workerCount int) int
}

// activeSchedulerProvider is implemented by the placement controller, the
// stable facade that actuators pass to Schedule. It returns the concrete
// scheduler for the active policy, or nil while a reload is in progress.
type activeSchedulerProvider interface {
	Active() autoscaling.PlacementScheduler
}

type PlacementState struct {
	workers               []workerPlacementState
	startTokensPerWorker  uint64
	startTokensTotal      uint64
	maxInstancesPerWorker uint64
	pendingStarts         atomic.Uint64
	startCapacityChanged  chan struct{}
	reservationCursor     atomic.Uint64
	reservations          []placementReservationSlot
	// trackImages gates leaf-local image bookkeeping (pendingImages,
	// knownImages). Only image-aware placement reads it, so a non-image-aware
	// policy must not pay to maintain it. The placement controller sets this on
	// every policy reload.
	trackImages atomic.Bool
}

type workerPlacementState struct {
	telemetry     atomic.Pointer[core.WorkerState]
	instances     atomic.Uint64
	pendingStarts atomic.Uint64

	imageMu       sync.Mutex
	pendingImages map[string]int
	knownImages   map[string]struct{}
}

type placementReservationSlot struct {
	// Even is free and odd is reserved. Completion advances the generation,
	// which makes duplicate calls with an old copied token harmless.
	state atomic.Uint64
}

func NewPlacementState(workerCount int, startTokensPerWorker uint32, startTokensTotal uint64, maxInstancesPerWorker uint32) *PlacementState {
	if workerCount < 0 {
		workerCount = 0
	}
	slots := workerCount * 64
	if startTokensTotal > 0 {
		slots = int(startTokensTotal)
	}
	if slots < 1 {
		slots = 1
	}
	return &PlacementState{
		workers:               make([]workerPlacementState, workerCount),
		startTokensPerWorker:  uint64(startTokensPerWorker),
		startTokensTotal:      startTokensTotal,
		maxInstancesPerWorker: uint64(maxInstancesPerWorker),
		startCapacityChanged:  make(chan struct{}, 1),
		reservations:          make([]placementReservationSlot, slots),
	}
}

// UpdateWorker publishes immutable telemetry outside placement calls.
func (p *PlacementState) UpdateWorker(index int, state *core.WorkerState) {
	if p == nil || index < 0 || index >= len(p.workers) || state == nil {
		return
	}
	p.workers[index].telemetry.Store(state)
}

// SetTrackImages toggles leaf-local image bookkeeping. The placement
// controller calls it on reload so only an image-aware policy pays for the
// pending/known image maps.
func (p *PlacementState) SetTrackImages(track bool) {
	if p == nil {
		return
	}
	p.trackImages.Store(track)
}

// ResetTelemetry drops the previous policy's worker telemetry and leaf-local
// image state. The placement controller calls it at the start of a reload so
// the new policy never reads signals retained for the old one. It is safe
// because placement work is stopped during a reload.
func (p *PlacementState) ResetTelemetry() {
	if p == nil {
		return
	}
	for i := range p.workers {
		p.workers[i].telemetry.Store(nil)
		p.workers[i].imageMu.Lock()
		p.workers[i].pendingImages = nil
		p.workers[i].knownImages = nil
		p.workers[i].imageMu.Unlock()
	}
}

func (p *PlacementState) StartCapacityChanged() <-chan struct{} {
	return p.startCapacityChanged
}

func (p *PlacementState) notifyStartCapacityChanged() {
	select {
	case p.startCapacityChanged <- struct{}{}:
	default:
	}
}

func (p *PlacementState) Schedule(ctx context.Context, scheduler autoscaling.PlacementScheduler, function *core.FunctionSpec, _ []*core.WorkerState, demand *core.ScaleDemand) (autoscaling.PlacementChoice, autoscaling.PlacementReservation, []*core.WorkerState, autoscaling.ScheduleTiming, error) {
	var emptyReservation autoscaling.PlacementReservation
	var timing autoscaling.ScheduleTiming
	// Actuators hold a stable facade (the placement controller) that publishes
	// the current concrete scheduler. Unwrap it so the balanced fast path and the
	// nil-while-reloading check still work.
	if provider, ok := scheduler.(activeSchedulerProvider); ok {
		scheduler = provider.Active()
	}
	if scheduler == nil {
		return autoscaling.PlacementChoice{Reason: "scheduler not configured"}, emptyReservation, nil, timing, nil
	}
	start := time.Now()
	if balanced, ok := scheduler.(balancedFastPath); ok {
		choice, reservation, err := p.scheduleBalanced(balanced, function)
		timing.PickMs = time.Since(start).Milliseconds()
		return choice, reservation, nil, timing, err
	}
	return p.scheduleSnapshot(ctx, scheduler, function, demand)
}

func (p *PlacementState) scheduleSnapshot(ctx context.Context, scheduler autoscaling.PlacementScheduler, function *core.FunctionSpec, demand *core.ScaleDemand) (choice autoscaling.PlacementChoice, reservation autoscaling.PlacementReservation, snapshot []*core.WorkerState, timing autoscaling.ScheduleTiming, err error) {
	snapshot = p.snapshot()
	if !p.reserveGlobalStart() {
		err = autoscaling.ErrStartSlotsExhausted
		return
	}
	defer func() {
		if !reservation.Valid {
			p.releaseGlobalStart()
		}
	}()
	pickStart := time.Now()
	picked, pickErr := scheduler.PickWorker(ctx, function, snapshot, demand)
	timing.PickMs = time.Since(pickStart).Milliseconds()
	if pickErr != nil {
		err = pickErr
		return
	}
	if picked == nil || picked.GetWorkerId() == 0 {
		if picked != nil {
			choice.Reason = picked.GetReason()
		}
		return
	}
	choice = autoscaling.PlacementChoice{WorkerID: picked.GetWorkerId(), Reason: picked.GetReason()}
	idx := int(choice.WorkerID) - 1
	if !p.reserveWorker(idx) {
		choice = autoscaling.PlacementChoice{Reason: "worker capacity changed during placement"}
		err = autoscaling.ErrStartSlotsExhausted
		return
	}
	reservation, err = p.newReservation(idx, functionImage(function))
	if err != nil {
		decrement(&p.workers[idx].pendingStarts)
	}
	return
}

func (p *PlacementState) scheduleBalanced(scheduler balancedFastPath, function *core.FunctionSpec) (autoscaling.PlacementChoice, autoscaling.PlacementReservation, error) {
	var reservation autoscaling.PlacementReservation
	n := len(p.workers)
	if n == 0 {
		return autoscaling.PlacementChoice{Reason: "no workers"}, reservation, nil
	}
	if !p.reserveGlobalStart() {
		return autoscaling.PlacementChoice{}, reservation, autoscaling.ErrStartSlotsExhausted
	}
	start := scheduler.NextWorkerIndex(n)
	for offset := range n {
		idx := (start + offset) % n
		if !p.reserveWorker(idx) {
			continue
		}
		var err error
		reservation, err = p.newReservation(idx, functionImage(function))
		if err != nil {
			decrement(&p.workers[idx].pendingStarts)
			p.releaseGlobalStart()
			return autoscaling.PlacementChoice{}, reservation, err
		}
		return autoscaling.PlacementChoice{WorkerID: uint64(idx + 1), Reason: "balanced-round-robin"}, reservation, nil
	}
	p.releaseGlobalStart()
	return autoscaling.PlacementChoice{Reason: "no worker capacity"}, reservation, autoscaling.ErrStartSlotsExhausted
}

func (p *PlacementState) newReservation(workerIdx int, image string) (autoscaling.PlacementReservation, error) {
	reservation, ok := p.claimReservationSlot()
	if !ok {
		return autoscaling.PlacementReservation{}, autoscaling.ErrStartSlotsExhausted
	}
	reservation.WorkerIndex = workerIdx
	reservation.Image = image
	reservation.Valid = true
	if image != "" && p.trackImages.Load() {
		w := &p.workers[workerIdx]
		w.imageMu.Lock()
		if w.pendingImages == nil {
			w.pendingImages = make(map[string]int)
		}
		w.pendingImages[image]++
		w.imageMu.Unlock()
	}
	return reservation, nil
}

func (p *PlacementState) claimReservationSlot() (autoscaling.PlacementReservation, bool) {
	n := len(p.reservations)
	start := int((p.reservationCursor.Add(1) - 1) % uint64(n))
	for offset := range n {
		idx := (start + offset) % n
		slot := &p.reservations[idx]
		state := slot.state.Load()
		if state&1 != 0 || !slot.state.CompareAndSwap(state, state+1) {
			continue
		}
		return autoscaling.PlacementReservation{Slot: idx, Generation: state + 1}, true
	}
	return autoscaling.PlacementReservation{}, false
}

func (p *PlacementState) reserveGlobalStart() bool {
	for {
		current := p.pendingStarts.Load()
		if p.startTokensTotal > 0 && current >= p.startTokensTotal {
			return false
		}
		if p.pendingStarts.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (p *PlacementState) reserveWorker(idx int) bool {
	if idx < 0 || idx >= len(p.workers) {
		return false
	}
	w := &p.workers[idx]
	for {
		if !p.workerEligible(idx) {
			return false
		}
		pending := w.pendingStarts.Load()
		if p.startTokensPerWorker > 0 && pending >= p.startTokensPerWorker {
			return false
		}
		if p.maxInstancesPerWorker > 0 && p.observedInstances(idx)+pending >= p.maxInstancesPerWorker {
			return false
		}
		if !w.pendingStarts.CompareAndSwap(pending, pending+1) {
			continue
		}
		if !p.workerEligible(idx) || (p.maxInstancesPerWorker > 0 && p.observedInstances(idx)+pending+1 > p.maxInstancesPerWorker) {
			decrement(&w.pendingStarts)
			return false
		}
		return true
	}
}

func (p *PlacementState) workerEligible(idx int) bool {
	state := p.workers[idx].telemetry.Load()
	return state == nil || (state.GetHealthy() && state.GetSchedulable())
}

func (p *PlacementState) observedInstances(idx int) uint64 {
	local := p.workers[idx].instances.Load()
	if state := p.workers[idx].telemetry.Load(); state != nil && state.GetInstances() > local {
		return state.GetInstances()
	}
	return local
}

func functionImage(function *core.FunctionSpec) string {
	if function == nil || function.GetRuntime() == nil {
		return ""
	}
	return strings.TrimSpace(function.GetRuntime().GetImage())
}

func (p *PlacementState) RecordStop(workerID uint64) {
	idx := int(workerID) - 1
	if p == nil || idx < 0 || idx >= len(p.workers) {
		return
	}
	decrement(&p.workers[idx].instances)
}

func (p *PlacementState) CommitReservation(reservation autoscaling.PlacementReservation) {
	if !p.completeReservation(reservation) {
		return
	}
	w := &p.workers[reservation.WorkerIndex]
	decrement(&w.pendingStarts)
	w.instances.Add(1)
	p.finishImage(reservation, true)
	p.releaseGlobalStart()
	p.notifyStartCapacityChanged()
}

func (p *PlacementState) CancelReservation(reservation autoscaling.PlacementReservation) {
	if !p.completeReservation(reservation) {
		return
	}
	w := &p.workers[reservation.WorkerIndex]
	decrement(&w.pendingStarts)
	p.finishImage(reservation, false)
	p.releaseGlobalStart()
	p.notifyStartCapacityChanged()
}

func (p *PlacementState) completeReservation(reservation autoscaling.PlacementReservation) bool {
	if !reservation.Valid || reservation.Slot < 0 || reservation.Slot >= len(p.reservations) ||
		reservation.WorkerIndex < 0 || reservation.WorkerIndex >= len(p.workers) {
		return false
	}
	slot := &p.reservations[reservation.Slot]
	return slot.state.CompareAndSwap(reservation.Generation, reservation.Generation+1)
}

func (p *PlacementState) finishImage(reservation autoscaling.PlacementReservation, committed bool) {
	if reservation.Image == "" {
		return
	}
	w := &p.workers[reservation.WorkerIndex]
	w.imageMu.Lock()
	if n := w.pendingImages[reservation.Image]; n <= 1 {
		delete(w.pendingImages, reservation.Image)
	} else {
		w.pendingImages[reservation.Image] = n - 1
	}
	if committed && p.trackImages.Load() {
		if w.knownImages == nil {
			w.knownImages = make(map[string]struct{})
		}
		w.knownImages[reservation.Image] = struct{}{}
	}
	w.imageMu.Unlock()
}

func (p *PlacementState) snapshot() []*core.WorkerState {
	out := make([]*core.WorkerState, len(p.workers))
	for i := range p.workers {
		w := &p.workers[i]
		telemetry := w.telemetry.Load()
		state := &core.WorkerState{WorkerId: uint64(i + 1), Healthy: true, Schedulable: true}
		if telemetry != nil {
			state.Healthy = telemetry.GetHealthy()
			state.Schedulable = telemetry.GetSchedulable()
			state.Capacity = telemetry.GetCapacity()
			state.Allocated = telemetry.GetAllocated()
			state.LoadAverageNorm = telemetry.GetLoadAverageNorm()
			state.CachedImages = telemetry.GetCachedImages()
		}
		pending := w.pendingStarts.Load()
		state.Instances = p.observedInstances(i) + pending
		state.ColdStartsInFlight = uint32(pending)
		if (p.startTokensPerWorker > 0 && pending >= p.startTokensPerWorker) ||
			(p.maxInstancesPerWorker > 0 && state.Instances >= p.maxInstancesPerWorker) {
			state.Schedulable = false
		}
		if p.trackImages.Load() {
			w.imageMu.Lock()
			state.CachedImages = appendKnownImages(state.CachedImages, w.pendingImages, w.knownImages)
			w.imageMu.Unlock()
		}
		out[i] = state
	}
	return out
}

func appendKnownImages(base []*core.CachedImage, pending map[string]int, known map[string]struct{}) []*core.CachedImage {
	if len(pending) == 0 && len(known) == 0 {
		return base
	}
	out := append([]*core.CachedImage(nil), base...)
	have := make(map[string]struct{}, len(base)+len(pending)+len(known))
	for _, image := range base {
		if image != nil {
			have[strings.TrimSpace(image.GetImage())] = struct{}{}
			have[strings.TrimSpace(image.GetDigest())] = struct{}{}
		}
	}
	add := func(ref string) {
		if ref == "" {
			return
		}
		if _, ok := have[ref]; ok {
			return
		}
		have[ref] = struct{}{}
		out = append(out, &core.CachedImage{Image: ref})
	}
	for ref := range pending {
		add(ref)
	}
	for ref := range known {
		add(ref)
	}
	return out
}

func (p *PlacementState) overlay(workers []*core.WorkerState) []*core.WorkerState {
	for i, worker := range workers {
		if worker != nil && i < len(p.workers) {
			p.UpdateWorker(i, worker)
		}
	}
	return p.snapshot()
}

func (p *PlacementState) releaseGlobalStart() {
	decrement(&p.pendingStarts)
}

func decrement(value *atomic.Uint64) {
	for current := value.Load(); current > 0; current = value.Load() {
		if value.CompareAndSwap(current, current-1) {
			return
		}
	}
}
