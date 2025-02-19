package scheduling

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
)

var workerNames = make([]state.WorkerID, 10)

func BenchmarkSyncMapScheduler1(b *testing.B) {
	//scheduler := CreateTestStateSyncMap()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for i := 0; i < 10; i++ {
			updateWorker(scheduler)
			time.Sleep(100 * time.Millisecond)
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		for i := 0; i < 10; i++ {
			deleteWorker(scheduler)
			time.Sleep(200 * time.Millisecond)
		}
		wg.Done()
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
				// Here in the API code we call the worker to start a new instance
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateNew)
			} else {
				// An Idle instance was found
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateRunning)
			}
			// Here in the API we forward the request to the worker
			time.Sleep(10 * time.Millisecond)
			// The instance is no longer running
			scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateIdle)
		}
	})

	wg.Wait()
	//scheduler.workers.DebugPrint()
}

/*
Benchmark design:
- every 5 seconds create a worker

- attempt to schedule calls with benchmarking function

- every 6 seconds remove a worker
- every 3 seconds remove a function
*/
func updateWorker(scheduler *syncMapScheduler) {
	randString := strconv.Itoa(rand.Intn(10))
	scheduler.UpdateWorkerState(state.WorkerID(randString), state.WorkerStateUp)
	workerNames = append(workerNames, state.WorkerID(randString))
}
func updateWorker2(scheduler *mruScheduler) {
	randString := strconv.Itoa(rand.Intn(10))
	scheduler.UpdateWorkerState(state.WorkerID(randString), state.WorkerStateUp)
	workerNames = append(workerNames, state.WorkerID(randString))
}

func deleteWorker(scheduler *syncMapScheduler) {
	randWorker := workerNames[rand.Intn(len(workerNames))]
	scheduler.UpdateWorkerState(randWorker, state.WorkerStateDown)
	workerNames = remove(workerNames, randWorker)
}

func deleteWorker2(scheduler *mruScheduler) {
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

func BenchmarkSyncMapScheduler2(b *testing.B) {
	//scheduler := CreateTestStateSyncMap()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewMRUScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	// every 5 seconds create a worker in parallel
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for i := 0; i < 10; i++ {
			updateWorker2(scheduler)
			time.Sleep(100 * time.Millisecond)
		}
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		for i := 0; i < 10; i++ {
			deleteWorker2(scheduler)
			time.Sleep(200 * time.Millisecond)
		}
		wg.Done()
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
				// Here in the API code we call the worker to start a new instance
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateNew)
			} else {
				// An Idle instance was found
				scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateRunning)
			}
			// Here in the API we forward the request to the worker
			time.Sleep(10 * time.Millisecond)
			// The instance is no longer running
			scheduler.UpdateInstanceState(workerID, state.FunctionID(randFunctionID), instanceID, state.InstanceStateIdle)
		}
	})

	wg.Wait()

	//scheduler.workers.DebugPrint()
}
