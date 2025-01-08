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
	scheduler := NewNaiveScheduler([]string{"worker1", "worker2"}, logger)
	firstState := CreateTestState()
	err := scheduler.UpdateState(context.Background(), &firstState)
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	assert.Equal(t, scheduler.workerState, firstState, "Worker state should be the same as the state passed in")

	secondState := state.WorkerStateMap{
		"worker1": []state.FunctionState{
			{FunctionID: "func1", Idle: []state.InstanceState{
				{InstanceID: "instance1"},
			}},
		},
	}

	err = scheduler.UpdateState(context.Background(), &secondState)
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	assert.Equal(t, scheduler.workerState, secondState, "Worker state should be updated")
}

func TestNaiveSchedulerSchedule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewNaiveScheduler([]string{"worker1", "worker2"}, logger)
	state := CreateTestState()
	err := scheduler.UpdateState(context.Background(), &state)
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	decision, funcID, err := scheduler.Schedule(context.Background(), "func1")
	if err != nil {
		t.Fatalf("Error scheduling call: %v", err)
	}

	assert.Equal(t, string(decision), "worker1", "Call should be scheduled to worker1")
	assert.Equal(t, funcID, "func1", "Function ID should be func1")
}

func TestMRUSchedulerSchedule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scheduler := NewMRUScheduler([]string{"worker1", "worker2"}, logger)
	state := CreateTestState()
	err := scheduler.UpdateState(context.Background(), &state)
	if err != nil {
		t.Fatalf("Error updating state: %v", err)
	}

	decision, funcID, err := scheduler.Schedule(context.Background(), "func1")
	if err != nil {
		t.Fatalf("Error scheduling call: %v", err)
	}

	assert.Equal(t, string(decision), "worker1", "Call should be scheduled to worker1")
	assert.Equal(t, funcID, "func1", "Function ID should be func1")
}
