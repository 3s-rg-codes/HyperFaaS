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
		"worker1": []state.Function{
			{FunctionID: "func1", Idle: []state.Instance{
				{InstanceID: "instance1",
					LastWorked: time.Unix(6, 0),
					Created:    time.Unix(5, 0),
				},
				{InstanceID: "instance2",
					LastWorked: time.Unix(6, 0),
					Created:    time.Unix(4, 0),
				},
				{InstanceID: "instance3",
					LastWorked: time.Unix(8, 0),
					Created:    time.Unix(5, 0),
				},
			},
				Running: []state.Instance{
					{InstanceID: "instance4",
						LastWorked: time.Unix(11, 0),
						Created:    time.Unix(10, 0),
					},
					{InstanceID: "instance5",
						LastWorked: time.Unix(12, 0),
						Created:    time.Unix(10, 0),
					},
				},
			},
		},
		"worker2": []state.Function{
			{FunctionID: "func2", Idle: []state.Instance{
				{InstanceID: "instance6",
					LastWorked: time.Unix(13, 0),
					Created:    time.Unix(12, 0),
				},
				{InstanceID: "instance7",
					LastWorked: time.Unix(14, 0),
					Created:    time.Unix(16, 0),
				},
			},
				Running: []state.Instance{
					{InstanceID: "instance8",
						LastWorked: time.Unix(17, 0),
						Created:    time.Unix(18, 0),
					},
					{InstanceID: "instance9",
						LastWorked: time.Unix(19, 0),
						Created:    time.Unix(20, 0),
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
	scheduler.UpdateInstanceState("worker1", "func1", "instance99", state.InstanceStateNew)

	t.Logf("Worker state: %v", scheduler.workerState)
	// Not sure if the struct comparison will work as intended here bc the struct is created when we call UpdateInstanceState
	assert.Contains(t, scheduler.workerState["worker1"][0].Idle, state.Instance{InstanceID: "instance99"})

	scheduler.UpdateInstanceState("worker1", "func1", "instance2", state.InstanceStateRunning)

	// func1 should be running on instance2
	assert.Contains(t, scheduler.workerState["worker1"][0].Running, state.Instance{InstanceID: "instance2"})
	assert.NotContains(t, scheduler.workerState["worker1"][0].Idle, state.Instance{InstanceID: "instance2"})
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
