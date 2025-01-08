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

func CreateTestState() state.WorkerStateMap {
	// The MRU instance is instance1 for func1 on worker1
	return state.WorkerStateMap{
		"worker1": []state.FunctionState{
			{FunctionID: "func1", Idle: []state.InstanceState{
				{InstanceID: "instance1",
					TimeSinceLastWork: 1 * time.Second,
					Uptime:            5 * time.Second,
				},
				{InstanceID: "instance2",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            4 * time.Second,
				},
				{InstanceID: "instance3",
					TimeSinceLastWork: 3 * time.Second,
					Uptime:            5 * time.Second,
				},
			},
				Running: []state.InstanceState{
					{InstanceID: "instance4",
						TimeSinceLastWork: 4 * time.Second,
						Uptime:            10 * time.Second,
					},
					{InstanceID: "instance5",
						TimeSinceLastWork: 5 * time.Second,
						Uptime:            10 * time.Second,
					},
				},
			},
		},
		"worker2": []state.FunctionState{
			{FunctionID: "func2", Idle: []state.InstanceState{
				{InstanceID: "instance6",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            5 * time.Second,
				},
				{InstanceID: "instance7",
					TimeSinceLastWork: 2 * time.Second,
					Uptime:            4 * time.Second,
				},
			},
				Running: []state.InstanceState{
					{InstanceID: "instance8",
						TimeSinceLastWork: 3 * time.Second,
						Uptime:            5 * time.Second,
					},
					{InstanceID: "instance9",
						TimeSinceLastWork: 4 * time.Second,
						Uptime:            5 * time.Second,
					},
				},
			},
		},
	}
}

func TestNaiveSchedulerUpdateState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	initialState := CreateTestState()
	scheduler := NewNaiveScheduler(initialState, []state.WorkerID{"worker1", "worker2"}, logger)
	err := scheduler.UpdateState(context.Background(), "worker1", "func1", "instance1")
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	// func1 should be running on instance1 . its appended to the end of the running list
	// Worker state: map[worker1:[{func1 [{instance4 false 4s 10s} {instance5 false 5s 10s}] [{instance2 false 2s 4s} {instance3 false 3s 5s} {instance3 false 3s 5s}]}] worker2:[{func2 [{instance8 false 3s 5s} {instance9 false 4s 5s}] [{instance6 false 2s 5s} {instance7 false 2s 4s}]}]]
	t.Logf("Worker state: %v", scheduler.workerState)
	assert.Equal(t, state.InstanceID("instance1"), scheduler.workerState["worker1"][0].Running[2].InstanceID, "func1 should be running on instance1")

	err = scheduler.UpdateState(context.Background(), "worker1", "func1", "instance2")
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	// func1 should be running on instance2
	assert.Equal(t, state.InstanceID("instance2"), scheduler.workerState["worker1"][0].Running[3].InstanceID, "func1 should be running on instance2")
}

func TestNaiveSchedulerSchedule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	initialState := CreateTestState()
	scheduler := NewNaiveScheduler(initialState, []state.WorkerID{"worker1", "worker2"}, logger)

	decision, _, err := scheduler.Schedule(context.Background(), "func1")
	if err != nil {
		t.Fatalf("Error scheduling call: %v", err)
	}

	assert.Equal(t, state.WorkerID("worker1"), decision, "Call should be scheduled to worker1")
}

func TestMRUSchedulerSchedule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	initialState := CreateTestState()
	scheduler := NewMRUScheduler(initialState, []state.WorkerID{"worker1", "worker2"}, logger)

	decision, instanceID, err := scheduler.Schedule(context.Background(), "func1")
	if err != nil {
		t.Fatalf("Error scheduling call: %v", err)
	}

	assert.Equal(t, state.WorkerID("worker1"), decision, "Call should be scheduled to worker1")
	assert.Equal(t, state.InstanceID("instance1"), instanceID, "Instance ID should be instance1")
}
