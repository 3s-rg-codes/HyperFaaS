package state

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
)

// MockWorkerControllerClient implements pb.ControllerClient
/* type ControllerClient interface {
    Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*InstanceID, error)
    Call(ctx context.Context, in *CallRequest, opts ...grpc.CallOption) (*Response, error)
    Stop(ctx context.Context, in *InstanceID, opts ...grpc.CallOption) (*InstanceID, error)
    Status(ctx context.Context, in *StatusRequest, opts ...grpc.CallOption) (Controller_StatusClient, error)
    Metrics(ctx context.Context, in *MetricsRequest, opts ...grpc.CallOption) (*MetricsUpdate, error)
    State(ctx context.Context, in *StateRequest, opts ...grpc.CallOption) (*StateResponse, error)
} */
type MockWorkerControllerClient struct {
	mock.Mock
}

func (m *MockWorkerControllerClient) State(ctx context.Context, req *controller.StateRequest, opts ...grpc.CallOption) (*controller.StateResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*controller.StateResponse), args.Error(1)
}
func TestConvertStateResponseToWorkerState(t *testing.T) {
	tests := []struct {
		name     string
		input    *controller.StateResponse
		expected []FunctionState
	}{
		{
			name: "empty response",
			input: &controller.StateResponse{
				Functions: []*controller.FunctionState{},
			},
			expected: []FunctionState{},
		},
		{
			name: "single function with instances",
			input: &controller.StateResponse{
				Functions: []*controller.FunctionState{
					{
						FunctionId: "func1",
						Running: []*controller.InstanceState{
							{
								InstanceId:        "instance1",
								IsActive:          true,
								TimeSinceLastWork: 1000, // 1 second in milliseconds
								Uptime:            5000, // 5 seconds in milliseconds
							},
						},
						Idle: []*controller.InstanceState{
							{
								InstanceId:        "instance2",
								IsActive:          false,
								TimeSinceLastWork: 2000,
								Uptime:            3000,
							},
						},
					},
				},
			},
			expected: []FunctionState{
				{
					FunctionID: "func1",
					Running: []InstanceState{
						{
							InstanceID:        "instance1",
							IsActive:          true,
							TimeSinceLastWork: 1 * time.Second,
							Uptime:            5 * time.Second,
						},
					},
					Idle: []InstanceState{
						{
							InstanceID:        "instance2",
							IsActive:          false,
							TimeSinceLastWork: 2 * time.Second,
							Uptime:            3 * time.Second,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStateResponseToWorkerState(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScraperGetWorkerState(t *testing.T) {
	mockClient := new(MockWorkerControllerClient)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	s := &scraper{
		workerConnections: map[WorkerID]controller.ControllerClient{
			"worker1": mockClient,
		},
		logger: logger,
	}

	expectedResponse := &controller.StateResponse{
		Functions: []*controller.FunctionState{
			{
				FunctionId: "func1",
				Running: []*controller.InstanceState{
					{
						InstanceId:        "instance1",
						IsActive:          true,
						TimeSinceLastWork: 1000,
						Uptime:            5000,
					},
				},
			},
		},
	}

	// Configure mock: when State() is called with any context and the leaf leader ID,
	// return our expectedResponse with no erro
	mockClient.On("State", mock.Anything, &controller.StateRequest{NodeId: leafLeaderID}).
		Return(expectedResponse, nil)

	state, err := s.GetWorkerState("worker1")

	assert.NoError(t, err)
	assert.Len(t, state, 1)

	// Check if the state is as expected
	assert.Equal(t, FunctionID("func1"), state[0].FunctionID)
	assert.Len(t, state[0].Running, 1)
	assert.Equal(t, InstanceID("instance1"), state[0].Running[0].InstanceID)
	assert.Equal(t, 1*time.Second, state[0].Running[0].TimeSinceLastWork)
	assert.Equal(t, 5*time.Second, state[0].Running[0].Uptime)

	mockClient.AssertExpectations(t)
}

func TestScraper_Scrape(t *testing.T) {
	mockClient := new(MockWorkerControllerClient)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	s := &scraper{
		workerIDs: []WorkerID{"worker1", "worker2"},
		workerConnections: map[WorkerID]controller.ControllerClient{
			"worker1": mockClient,
			"worker2": mockClient,
		},
		state:          make(WorkerStateMap),
		logger:         logger,
		scrapeInterval: 1 * time.Second,
	}

	expectedResponse := &controller.StateResponse{
		Functions: []*controller.FunctionState{
			{
				FunctionId: "func1",
				Running: []*controller.InstanceState{
					{
						InstanceId:        "instance1",
						IsActive:          true,
						TimeSinceLastWork: 1000,
						Uptime:            5000,
					},
				},
			},
		},
	}

	mockClient.On("State", mock.Anything, &controller.StateRequest{NodeId: leafLeaderID}).
		Return(expectedResponse, nil)

	state, err := s.Scrape(context.Background())

	assert.NoError(t, err)
	assert.Len(t, state, 2)
	assert.Contains(t, state, WorkerID("worker1"))
	assert.Contains(t, state, WorkerID("worker2"))

	// Check if the state is as expected
	assert.Equal(t, FunctionID("func1"), state["worker1"][0].FunctionID)
	assert.Equal(t, FunctionID("func1"), state["worker2"][0].FunctionID)
	assert.Len(t, state["worker1"][0].Running, 1)
	assert.Len(t, state["worker2"][0].Running, 1)
	assert.Equal(t, InstanceID("instance1"), state["worker1"][0].Running[0].InstanceID)
	assert.Equal(t, InstanceID("instance1"), state["worker2"][0].Running[0].InstanceID)
	assert.Equal(t, 1*time.Second, state["worker1"][0].Running[0].TimeSinceLastWork)
	assert.Equal(t, 1*time.Second, state["worker2"][0].Running[0].TimeSinceLastWork)
	assert.Equal(t, 5*time.Second, state["worker1"][0].Running[0].Uptime)
	assert.Equal(t, 5*time.Second, state["worker2"][0].Running[0].Uptime)

	mockClient.AssertExpectations(t)
}

func (m *MockWorkerControllerClient) Call(ctx context.Context, in *common.CallRequest, opts ...grpc.CallOption) (*common.CallResponse, error) {
	return nil, nil
}
func (m *MockWorkerControllerClient) Metrics(ctx context.Context, in *controller.MetricsRequest, opts ...grpc.CallOption) (*controller.MetricsUpdate, error) {
	return nil, nil
}
func (m *MockWorkerControllerClient) Start(ctx context.Context, in *controller.StartRequest, opts ...grpc.CallOption) (*common.InstanceID, error) {
	return nil, nil
}
func (m *MockWorkerControllerClient) Stop(ctx context.Context, in *common.InstanceID, opts ...grpc.CallOption) (*common.InstanceID, error) {
	return nil, nil
}
func (m *MockWorkerControllerClient) Status(ctx context.Context, in *controller.StatusRequest, opts ...grpc.CallOption) (controller.Controller_StatusClient, error) {
	return nil, nil
}
