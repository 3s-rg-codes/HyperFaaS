package scheduling

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

var workerNames = make([]state.WorkerID, 10)

func updateWorkerState(scheduler Scheduler) {
	randString := strconv.Itoa(rand.Intn(10))
	scheduler.UpdateWorkerState(state.WorkerID(randString), state.WorkerStateUp)
	workerNames = append(workerNames, state.WorkerID(randString))
}

func deleteWorkerState(scheduler Scheduler) {
	randWorker := workerNames[rand.Intn(len(workerNames))]
	scheduler.UpdateWorkerState(randWorker, state.WorkerStateDown)
	workerNames = remove(workerNames, randWorker)
}

func remove(slice []state.WorkerID, s state.WorkerID) []state.WorkerID {
	for i, v := range slice {
		if v == s {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func runBenchmark(b *testing.B, scheduler Scheduler) {
	wg := sync.WaitGroup{}

	// WorkerStateMap creation goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			updateWorkerState(scheduler)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// WorkerStateMap deletion goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			deleteWorkerState(scheduler)
			time.Sleep(200 * time.Millisecond)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			randFunctionID := strconv.Itoa(rand.Intn(10))
			workerID, instanceID, err := scheduler.Schedule(context.Background(), state.FunctionID(randFunctionID))
			if err != nil {
				b.Error(err)
			}

			if instanceID == "" {
				// There is no idle instance available
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateNew)
			} else {
				// An Idle instance was found
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateRunning)
			}

			time.Sleep(10 * time.Millisecond)
			scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateIdle)
		}
	})

	wg.Wait()
}

func BenchmarkSyncMapScheduler(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	runBenchmark(b, scheduler)
}

func BenchmarkMRUScheduler(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	scheduler := NewMRUScheduler(state.NewWorkers(logger), []state.WorkerID{"worker1", "worker2"}, logger)
	runBenchmark(b, scheduler)
}
