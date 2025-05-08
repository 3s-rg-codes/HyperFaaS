package scheduling

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/stretchr/testify/assert"
)

// CreateTestSyncMapScheduler initializes a test worker state
// The state is structured as follows:
//
// WorkerStateMap (sync.Map):
//
// ├── worker1 (key)
// │   ├── func1 (key)
// │   │   ├── Idle (key)
// │   │   │   ├── instance1 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   ├── instance2 → { LastWorked: ?, Created: ? } //2nd  most recently used
// │   │   │   └── instance3 → { LastWorked: ?, Created: ? } //Newest - Most recently used
// │   │   ├── Running (key)
// │   │   │   ├── instance4 → { LastWorked: ?, Created: ? } // Oldest
// │   │   │   └── instance5 → { LastWorked: ?, Created: ? } // Newest
// │   │   ├── func2 (key)
// │   │   │   ├── Idle (key)
// │   │   │   │   ├── instance6 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   │   └── instance7 → { LastWorked: ?, Created: ? } //Newest
// │   │   │   └── Running (key)
// │   │   │   │   ├── instance8 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   │   └── instance9 → { LastWorked: ?, Created: ? } //Newest
// │
// ├── worker2 (key)
// │   ├── func3 (key)
// │   │   ├── Idle (key)
// │   │   │   ├── instance10 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   └── instance11 → { LastWorked: ?, Created: ? } //Newest
// │   │   ├── Running (key)
// │   │   │   ├── instance12 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   └── instance13 → { LastWorked: ?, Created: ? } //Newest
// │
func CreateTestSyncMapScheduler() *syncMapScheduler {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	return AddTestInstances(scheduler).(*syncMapScheduler)
}

func CreateTestMRUScheduler() *mruScheduler {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	scheduler := NewMRUScheduler(state.NewWorkers(logger), []state.WorkerID{"worker1", "worker2"}, logger)
	return AddTestInstances(scheduler).(*mruScheduler)
}

func AddTestInstances(scheduler Scheduler) Scheduler {
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.CreateFunction("worker1", "func2")
	scheduler.CreateFunction("worker2", "func3")
	// Create instances for worker1
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance3", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance4", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance5", state.InstanceStateRunning)

	scheduler.UpdateInstanceState("worker1", "func2", "instance6", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func2", "instance7", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func2", "instance8", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func2", "instance9", state.InstanceStateRunning)

	// Create instances for worker2
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker2", "func3", "instance11", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker2", "func3", "instance12", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker2", "func3", "instance13", state.InstanceStateRunning)

	// Set some instances to idle for worker1
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)
	scheduler.UpdateInstanceState("worker1", "func1", "instance3", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)
	scheduler.UpdateInstanceState("worker1", "func2", "instance6", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)
	scheduler.UpdateInstanceState("worker1", "func2", "instance7", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)

	// Set some instances to idle for worker2
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)
	scheduler.UpdateInstanceState("worker2", "func3", "instance11", state.InstanceStateIdle)
	time.Sleep(10 * time.Millisecond)

	return scheduler
}
func TestSyncMapSchedulerUpdateWorkerState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)

	assert.NotNil(t, scheduler.workers.GetWorker("worker1"))
	assert.NotNil(t, scheduler.workers.GetWorker("worker2"))
}

func TestSyncMapSchedulerCreateFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.CreateFunction("worker1", "func2")
	scheduler.CreateFunction("worker2", "func3")
	scheduler.CreateFunction("worker2", "func1")

	fsm1, _ := scheduler.workers.GetFunctionStateMap("worker1", "func1")
	fsm2, _ := scheduler.workers.GetFunctionStateMap("worker1", "func2")
	fsm3, _ := scheduler.workers.GetFunctionStateMap("worker2", "func3")
	fsm4, _ := scheduler.workers.GetFunctionStateMap("worker2", "func1")

	assert.NotNil(t, fsm1)
	assert.NotNil(t, fsm2)
	assert.NotNil(t, fsm3)
	assert.NotNil(t, fsm4)

	// The Idle and Running slices should exist
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func2", state.InstanceStateIdle))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func2", state.InstanceStateRunning))
}

func TestSyncMapSchedulerUpdateInstanceState(t *testing.T) {
	scheduler := CreateTestSyncMapScheduler()
	//------------WorkerStateMap 1------------
	// Turn instance 1 to Running
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateRunning)
	// Should be 3 running and 2 idle
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 3)
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 2)
	// Turn instance 2 and 3 to Running
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance3", state.InstanceStateRunning)
	// Should be 5 running and 0 idle
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 5)
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 0)

	//------------WorkerStateMap 2------------
	// Turn instance 10 to Running
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateRunning)
	// Should be 3 running and 1 idle
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func3", state.InstanceStateRunning), 3)
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func3", state.InstanceStateIdle), 1)

}

func TestSyncMapSchedulerDeleteInstance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)

	// Set up initial state
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateRunning)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateRunning)

	// Verify initial state
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 2)

	// Delete an instance
	scheduler.workers.DeleteInstance("worker1", "func1", state.InstanceStateRunning, "instance1")

	// Verify instance was deleted
	instances := scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning)
	assert.Len(t, instances, 1)
	assert.Equal(t, state.InstanceID("instance2"), instances[0].InstanceID)
}

func TestSyncMapSchedulerSchedule(t *testing.T) {
	scheduler := CreateTestSyncMapScheduler()
	workerID, instanceID, err := scheduler.Schedule(context.Background(), "func1")

	// instance 3 should have been picked because it's the most recently used
	assert.NoError(t, err)
	assert.Equal(t, state.WorkerID("worker1"), workerID)
	assert.Equal(t, state.InstanceID("instance3"), instanceID)
	// now  4 ,5 and an additional instance should be running
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 3)
	// now just 2 should be idle
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 2)
}

func TestMRUSchedulerUpdateWorkerState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewMRUScheduler(state.NewWorkers(logger), []state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	worker := scheduler.workers.Workers["worker1"]
	assert.NotNil(t, worker)
	worker = scheduler.workers.Workers["worker2"]
	assert.NotNil(t, worker)
}

func TestMRUSchedulerUpdateInstanceState(t *testing.T) {
	scheduler := CreateTestMRUScheduler()
	//------------WorkerStateMap 1------------
	// Turn instance 1 to Running
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateRunning)
	// Should be 3 running and 2 idle
	worker := scheduler.workers.Workers["worker1"]
	assert.Equal(t, 2, countOccurences(worker.Functions["func1"], state.InstanceStateIdle))
	assert.Equal(t, 3, countOccurences(worker.Functions["func1"], state.InstanceStateRunning))

	//------------WorkerStateMap 2------------
	// Turn instance 10 to Running
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateRunning)
	// Should be 3 running and 1 idle
	worker = scheduler.workers.Workers["worker2"]
	assert.Equal(t, 1, countOccurences(worker.Functions["func3"], state.InstanceStateIdle))
	assert.Equal(t, 3, countOccurences(worker.Functions["func3"], state.InstanceStateRunning))

}

func TestMRUSchedulerSchedule(t *testing.T) {
	scheduler := CreateTestMRUScheduler()

	//------------WorkerStateMap 1------------
	// There are 3 idle instances for func1
	workerID, instanceID, err := scheduler.Schedule(context.Background(), "func1")
	assert.NoError(t, err)
	assert.Equal(t, state.WorkerID("worker1"), workerID)
	assert.Equal(t, state.InstanceID("instance3"), instanceID)

	workerID, instanceID, err = scheduler.Schedule(context.Background(), "func1")
	assert.NoError(t, err)
	assert.Equal(t, state.WorkerID("worker1"), workerID)
	assert.Equal(t, state.InstanceID("instance2"), instanceID)

	workerID, instanceID, err = scheduler.Schedule(context.Background(), "func1")
	assert.NoError(t, err)
	assert.Equal(t, state.WorkerID("worker1"), workerID)
	assert.Equal(t, state.InstanceID("instance1"), instanceID)

	//Should return an empty instanceID because there are no idle instances left
	_, instanceID, err = scheduler.Schedule(context.Background(), "func1")
	assert.NoError(t, err)
	assert.Equal(t, state.InstanceID(""), instanceID)

}

func countOccurences(instances map[state.InstanceID]*state.Instance, state state.InstanceState) int {
	count := 0
	for _, inst := range instances {
		if inst.State == state {
			count++
		}
	}
	return count
}
