package scheduling

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	"github.com/stretchr/testify/assert"
)

// CreateTestStateSyncMap initializes a test worker state
// The state is structured as follows:
//
// WorkerStateMap (sync.Map):
//
// ├── worker1 (key)
// │   ├── func1 (key)
// │   │   ├── Idle (key)
// │   │   │   ├── instance1 → { LastWorked: ?, Created: ? } //Oldest
// │   │   │   ├── instance2 → { LastWorked: ?, Created: ? } //2nd oldest
// │   │   │   └── instance3 → { LastWorked: ?, Created: ? } //Newest
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
func CreateTestStateSyncMap() *syncMapScheduler {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.CreateFunction("worker1", "func2")
	scheduler.CreateFunction("worker2", "func3")
	// Create instances for worker1
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance3", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance4", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance5", state.InstanceStateNew)

	scheduler.UpdateInstanceState("worker1", "func2", "instance6", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func2", "instance7", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func2", "instance8", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func2", "instance9", state.InstanceStateNew)

	// Create instances for worker2
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker2", "func3", "instance11", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker2", "func3", "instance12", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker2", "func3", "instance13", state.InstanceStateNew)

	// Set some instances to idle for worker1
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker1", "func1", "instance3", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker1", "func2", "instance6", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker1", "func2", "instance7", state.InstanceStateIdle)

	// Set some instances to idle for worker2
	scheduler.UpdateInstanceState("worker2", "func3", "instance10", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker2", "func3", "instance11", state.InstanceStateIdle)

	return scheduler
}

func TestSchedulerMapUpdateWorkerState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)

	assert.NotNil(t, scheduler.workers.GetWorker("worker1"))
	assert.NotNil(t, scheduler.workers.GetWorker("worker2"))
}

func TestSchedulerMapCreateFunction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.CreateFunction("worker1", "func2")
	scheduler.CreateFunction("worker2", "func3")
	scheduler.CreateFunction("worker2", "func1")

	assert.NotNil(t, scheduler.workers.GetFunction("worker1", "func1"))
	assert.NotNil(t, scheduler.workers.GetFunction("worker1", "func2"))
	assert.NotNil(t, scheduler.workers.GetFunction("worker2", "func3"))
	assert.NotNil(t, scheduler.workers.GetFunction("worker2", "func1"))

	// The Idle and Running slices should exist
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func2", state.InstanceStateIdle))
	assert.NotNil(t, scheduler.workers.GetInstances("worker1", "func2", state.InstanceStateRunning))
}

func TestSchedulerMapUpdateInstanceState(t *testing.T) {
	// Create a test state - its unused for this test but the constructor requires it
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)
	// Register our test scenario:
	// worker1 has func1 and func2
	// worker2 has func3 and func1
	// func1 has instance1 and instance2
	// func2 has instance3
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.UpdateWorkerState("worker2", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.CreateFunction("worker1", "func2")

	scheduler.CreateFunction("worker2", "func3")
	scheduler.CreateFunction("worker2", "func1")

	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func2", "instance3", state.InstanceStateNew)

	scheduler.UpdateInstanceState("worker2", "func1", "instance1", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker2", "func1", "instance2", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker2", "func3", "instance3", state.InstanceStateNew)
	scheduler.workers.DebugPrint()
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 2)
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func1", state.InstanceStateRunning), 2)
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func3", state.InstanceStateRunning), 1)

	// Now turn them to idle
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateIdle)
	// Should be 1 running and 1 idle
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 1)
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 1)
	// Now everything should be idle
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker1", "func2", "instance3", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker2", "func1", "instance1", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker2", "func1", "instance3", state.InstanceStateIdle)
	scheduler.UpdateInstanceState("worker2", "func3", "instance2", state.InstanceStateIdle)

	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 2)
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func2", state.InstanceStateIdle), 1)
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func1", state.InstanceStateIdle), 2)
	assert.Len(t, scheduler.workers.GetInstances("worker2", "func3", state.InstanceStateIdle), 1)
}

func TestSchedulerMapDeleteInstance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewSyncMapScheduler([]state.WorkerID{"worker1", "worker2"}, logger)

	// Set up initial state
	scheduler.UpdateWorkerState("worker1", state.WorkerStateUp)
	scheduler.CreateFunction("worker1", "func1")
	scheduler.UpdateInstanceState("worker1", "func1", "instance1", state.InstanceStateNew)
	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateNew)

	// Verify initial state
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 2)

	// Delete an instance
	scheduler.workers.DeleteInstance("worker1", "func1", state.InstanceStateRunning, "instance1")

	// Verify instance was deleted
	instances := scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning)
	assert.Len(t, instances, 1)
	assert.Equal(t, state.InstanceID("instance2"), instances[0].InstanceID)
}

func TestSchedulerMapSchedule(t *testing.T) {
	scheduler := CreateTestStateSyncMap()
	workerID, instanceID, err := scheduler.Schedule(context.Background(), "func1") // either func1, func2, or func3 should be scheduled
	assert.NoError(t, err)
	assert.Equal(t, state.WorkerID("worker1"), workerID)
	assert.Equal(t, state.InstanceID("instance1"), instanceID)
	// now  4 ,5 and an additional instance should be running
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateRunning), 3)
	// now just 2 should be idle
	assert.Len(t, scheduler.workers.GetInstances("worker1", "func1", state.InstanceStateIdle), 2)
}
