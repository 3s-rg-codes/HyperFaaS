package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/autoscaling"
	"hyperfaas-ideal-arch/pkg/leaf/scheduler"
)

func TestPlacementStartTokensDisabledForDirigentAdmission(t *testing.T) {
	if got := placementStartTokens(leaf.DataplaneConfig{
		DirigentStrictAdmission: true,
		StartTokensPerWorker:    1,
	}); got != 0 {
		t.Fatalf("placementStartTokens()=%d, want 0 for Dirigent admission", got)
	}
}

func TestPlacementStartTokensUseSafeDefault(t *testing.T) {
	if got := placementStartTokens(leaf.DataplaneConfig{}); got != 8 {
		t.Fatalf("placementStartTokens()=%d, want 8", got)
	}
}

func TestPlacementStartLimitUsesExplicitOrDerivedLimit(t *testing.T) {
	if got := placementStartLimit(leaf.DataplaneConfig{MaxStartsPerReconcile: 3}, 2, 8); got != 3 {
		t.Fatalf("explicit placement start limit=%d, want 3", got)
	}
	if got := placementStartLimit(leaf.DataplaneConfig{}, 2, 8); got != 16 {
		t.Fatalf("derived placement start limit=%d, want 16", got)
	}
}

type recordingScheduler struct {
	snapshots [][]*core.WorkerState
}

func TestPlacementStateReportsTemporaryTokenExhaustionBeforeScheduling(t *testing.T) {
	state := NewPlacementState(1, 1, 1, 0)
	scheduler := &recordingScheduler{}
	workers := []*core.WorkerState{{WorkerId: 1, Healthy: true, Schedulable: true}}

	_, reservation, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("first Schedule reservation=%v error=%v", reservation, err)
	}
	_, _, _, _, err = state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if !errors.Is(err, autoscaling.ErrStartSlotsExhausted) {
		t.Fatalf("second Schedule error=%v, want %v", err, autoscaling.ErrStartSlotsExhausted)
	}
	if len(scheduler.snapshots) != 1 {
		t.Fatalf("scheduler calls=%d, want 1 while tokens are exhausted", len(scheduler.snapshots))
	}
	capacityChanged := state.StartCapacityChanged()
	state.CancelReservation(reservation)
	select {
	case <-capacityChanged:
	default:
		t.Fatal("token release did not notify waiting dispatchers")
	}
}

func (s *recordingScheduler) PickWorker(_ context.Context, _ *core.FunctionSpec, workers []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	s.snapshots = append(s.snapshots, workers)
	return &core.PlacementDecision{WorkerId: 1, Reason: "test"}, nil
}

func TestPlacementStateExposesReservationsAcrossSchedules(t *testing.T) {
	state := NewPlacementState(2, 0, 0, 0)
	scheduler := &recordingScheduler{}
	workers := []*core.WorkerState{
		{WorkerId: 1, Healthy: true, Schedulable: true},
		{WorkerId: 2, Healthy: true, Schedulable: true},
	}

	_, first, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil {
		t.Fatalf("first Schedule: %v", err)
	}
	if !first.Valid {
		t.Fatal("first Schedule returned nil reservation")
	}
	_, second, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil {
		t.Fatalf("second Schedule: %v", err)
	}
	if !second.Valid {
		t.Fatal("second Schedule returned nil reservation")
	}

	if got := scheduler.snapshots[1][0].GetColdStartsInFlight(); got != 1 {
		t.Fatalf("second snapshot cold starts on worker 1 = %d, want 1", got)
	}
	if got := scheduler.snapshots[1][0].GetInstances(); got != 1 {
		t.Fatalf("second snapshot instances on worker 1 = %d, want 1", got)
	}

	state.CommitReservation(first)
	state.CancelReservation(second)
	state.RecordStop(1)

	_, _, finalSnapshot, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil {
		t.Fatalf("final Schedule: %v", err)
	}
	if got := finalSnapshot[0].GetColdStartsInFlight(); got != 0 {
		t.Fatalf("final snapshot cold starts on worker 1 = %d, want 0", got)
	}
	if got := finalSnapshot[0].GetInstances(); got != 0 {
		t.Fatalf("final snapshot instances on worker 1 = %d, want 0", got)
	}
}

func TestPlacementLifecycleTransitionsKeepWorkerLedgerConsistent(t *testing.T) {
	state := NewPlacementState(1, 2, 2, 0)
	workers := []*core.WorkerState{{WorkerId: 1, Healthy: true, Schedulable: true}}
	scheduler := &recordingScheduler{}

	_, reservation, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("Schedule reservation=%v error=%v", reservation, err)
	}
	assertPlacementWorkerState(t, state.overlay(workers)[0], 1, 1)
	state.CommitReservation(reservation)
	assertPlacementWorkerState(t, state.overlay(workers)[0], 1, 0)
	state.RecordStop(1)
	assertPlacementWorkerState(t, state.overlay(workers)[0], 0, 0)
}

func assertPlacementWorkerState(t *testing.T, worker *core.WorkerState, instances uint64, coldStarts uint32) {
	t.Helper()
	if worker.GetInstances() != instances || worker.GetColdStartsInFlight() != coldStarts {
		t.Fatalf("instances/cold starts=%d/%d, want %d/%d", worker.GetInstances(), worker.GetColdStartsInFlight(), instances, coldStarts)
	}
}

type blockingScheduler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingScheduler) PickWorker(_ context.Context, _ *core.FunctionSpec, _ []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return &core.PlacementDecision{WorkerId: 1, Reason: "test"}, nil
}

func TestPlacementReservationCompletionDoesNotConvoyBehindScheduling(t *testing.T) {
	state := NewPlacementState(1, 0, 0, 0)
	workers := []*core.WorkerState{{WorkerId: 1, Healthy: true, Schedulable: true}}
	_, reservation, _, _, err := state.Schedule(context.Background(), &recordingScheduler{}, nil, workers, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("initial Schedule reservation=%v error=%v", reservation, err)
	}

	scheduler := &blockingScheduler{entered: make(chan struct{}), release: make(chan struct{})}
	scheduled := make(chan struct{})
	go func() {
		defer close(scheduled)
		_, next, _, _, _ := state.Schedule(context.Background(), scheduler, nil, workers, nil)
		if next.Valid {
			state.CancelReservation(next)
		}
	}()
	<-scheduler.entered

	done := make(chan struct{})
	go func() {
		state.CancelReservation(reservation)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("reservation completion waited for the placement lock")
	}
	close(scheduler.release)
	<-scheduled
}

type sequenceScheduler struct {
	workerIDs []uint64
	next      int
}

func (s *sequenceScheduler) PickWorker(_ context.Context, _ *core.FunctionSpec, _ []*core.WorkerState, _ *core.ScaleDemand) (*core.PlacementDecision, error) {
	workerID := s.workerIDs[s.next]
	s.next++
	return &core.PlacementDecision{WorkerId: workerID, Reason: "test"}, nil
}

func TestPlacementGlobalStartLimitRetriesAfterRelease(t *testing.T) {
	state := NewPlacementState(2, 2, 1, 0)
	workers := []*core.WorkerState{
		{WorkerId: 1, Healthy: true, Schedulable: true},
		{WorkerId: 2, Healthy: true, Schedulable: true},
	}
	scheduler := &sequenceScheduler{workerIDs: []uint64{1, 2}}
	_, first, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil || !first.Valid {
		t.Fatalf("first Schedule reservation=%v error=%v", first, err)
	}
	_, _, _, _, err = state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if !errors.Is(err, autoscaling.ErrStartSlotsExhausted) {
		t.Fatalf("second Schedule error=%v, want %v", err, autoscaling.ErrStartSlotsExhausted)
	}
	state.CancelReservation(first)
	_, retry, _, _, err := state.Schedule(context.Background(), scheduler, nil, workers, nil)
	if err != nil || !retry.Valid {
		t.Fatalf("retry Schedule reservation=%v error=%v", retry, err)
	}
	state.CancelReservation(retry)
}

func TestPlacementCompletionsOnDifferentWorkersDoNotConvoy(t *testing.T) {
	state := NewPlacementState(2, 2, 2, 0)
	workers := []*core.WorkerState{
		{WorkerId: 1, Healthy: true, Schedulable: true},
		{WorkerId: 2, Healthy: true, Schedulable: true},
	}
	scheduler := &sequenceScheduler{workerIDs: []uint64{1, 2}}
	function := &core.FunctionSpec{Runtime: &core.RuntimeSpec{Image: "test-image"}}
	_, first, _, _, _ := state.Schedule(context.Background(), scheduler, function, workers, nil)
	_, second, _, _, _ := state.Schedule(context.Background(), scheduler, function, workers, nil)

	state.workers[0].imageMu.Lock()
	firstDone := make(chan struct{})
	go func() {
		state.CancelReservation(first)
		close(firstDone)
	}()
	secondDone := make(chan struct{})
	go func() {
		state.CancelReservation(second)
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("worker 2 completion convoyed behind worker 1")
	}
	state.workers[0].imageMu.Unlock()
	<-firstDone
}

func TestPlacementConcurrentReservationsRespectInstanceCap(t *testing.T) {
	const attempts = 1000
	state := NewPlacementState(1, 0, 0, 5)
	state.UpdateWorker(0, &core.WorkerState{WorkerId: 1, Healthy: true, Schedulable: true})
	scheduler := schedulerForPlacementTest(1)

	reservations := make(chan autoscaling.PlacementReservation, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reservation, _, _, _ := state.Schedule(context.Background(), scheduler, nil, nil, nil)
			if reservation.Valid {
				reservations <- reservation
			}
		}()
	}
	wg.Wait()
	close(reservations)

	count := 0
	for reservation := range reservations {
		count++
		state.CancelReservation(reservation)
	}
	if count != 5 {
		t.Fatalf("successful reservations = %d, want exact cap 5", count)
	}
}

func TestPlacementReservationCompletionIsIdempotent(t *testing.T) {
	state := NewPlacementState(1, 1, 1, 1)
	state.UpdateWorker(0, &core.WorkerState{WorkerId: 1, Healthy: true, Schedulable: true})
	_, reservation, _, _, err := state.Schedule(context.Background(), schedulerForPlacementTest(1), nil, nil, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("Schedule reservation=%+v error=%v", reservation, err)
	}
	state.CommitReservation(reservation)
	state.CommitReservation(reservation)
	state.CancelReservation(reservation)
	if got := state.workers[0].instances.Load(); got != 1 {
		t.Fatalf("instances after duplicate completion = %d, want 1", got)
	}
	if got := state.pendingStarts.Load(); got != 0 {
		t.Fatalf("pending starts after duplicate completion = %d, want 0", got)
	}
}

func schedulerForPlacementTest(workers int) autoscaling.PlacementScheduler {
	return scheduler.NewBalancedRoundRobin(workers, 0)
}

// TestPlacementImageTrackingGatedByPolicy proves that leaf-local image state
// (pending/known images) is only maintained for an image-aware policy.
func TestPlacementImageTrackingGatedByPolicy(t *testing.T) {
	function := &core.FunctionSpec{FunctionId: 1, Runtime: &core.RuntimeSpec{Image: "gated-image"}}
	workers := []*core.WorkerState{{WorkerId: 1, Healthy: true, Schedulable: true}}

	off := NewPlacementState(1, 0, 0, 0)
	off.SetTrackImages(false)
	_, reservation, _, _, err := off.Schedule(context.Background(), schedulerForPlacementTest(1), function, workers, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("Schedule reservation=%v error=%v", reservation, err)
	}
	off.CommitReservation(reservation)
	if imgs := off.snapshot()[0].GetCachedImages(); len(imgs) != 0 {
		t.Fatalf("non-image-aware policy populated cached_images: %v", imgs)
	}

	on := NewPlacementState(1, 0, 0, 0)
	on.SetTrackImages(true)
	_, reservation, _, _, err = on.Schedule(context.Background(), schedulerForPlacementTest(1), function, workers, nil)
	if err != nil || !reservation.Valid {
		t.Fatalf("Schedule reservation=%v error=%v", reservation, err)
	}
	on.CommitReservation(reservation)
	if imgs := on.snapshot()[0].GetCachedImages(); len(imgs) != 1 {
		t.Fatalf("image-aware policy cached_images=%v, want one known image", imgs)
	}
}
