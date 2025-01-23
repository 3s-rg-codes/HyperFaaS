package state

import (
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		expected []Function
	}{
		{
			name: "empty response",
			input: &controller.StateResponse{
				Functions: []*controller.FunctionState{},
			},
			expected: []Function{},
		},
		{
			name: "single function with instances",
			input: &controller.StateResponse{
				Functions: []*controller.FunctionState{
					{
						FunctionId: "func1",
						Running: []*controller.InstanceState{
							{
								InstanceId: "instance1",
								IsActive:   true,
								Lastworked: timestamppb.New(time.Unix(1, 0).UTC()),
								Created:    timestamppb.New(time.Unix(5, 0).UTC()),
							},
						},
						Idle: []*controller.InstanceState{
							{
								InstanceId: "instance2",
								IsActive:   false,
								Lastworked: timestamppb.New(time.Unix(2, 0).UTC()),
								Created:    timestamppb.New(time.Unix(3, 0).UTC()),
							},
						},
					},
				},
			},
			expected: []Function{
				{
					FunctionID: "func1",
					Running: []Instance{
						{
							InstanceID: "instance1",
							IsActive:   true,
							LastWorked: time.Unix(1, 0).UTC(),
							Created:    time.Unix(5, 0).UTC(),
						},
					},
					Idle: []Instance{
						{
							InstanceID: "instance2",
							IsActive:   false,
							LastWorked: time.Unix(2, 0).UTC(),
							Created:    time.Unix(3, 0).UTC(),
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
						InstanceId: "instance1",
						IsActive:   true,
						Lastworked: timestamppb.New(time.Unix(1, 0).UTC()),
						Created:    timestamppb.New(time.Unix(5, 0).UTC()),
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
	assert.Equal(t, time.Unix(1, 0).UTC(), state[0].Running[0].LastWorked)
	assert.Equal(t, time.Unix(5, 0).UTC(), state[0].Running[0].Created)

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
						InstanceId: "instance1",
						IsActive:   true,
						Lastworked: timestamppb.New(time.Unix(1, 0).UTC()),
						Created:    timestamppb.New(time.Unix(5, 0).UTC()),
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
	assert.Equal(t, time.Unix(1, 0).UTC(), state["worker1"][0].Running[0].LastWorked)
	assert.Equal(t, time.Unix(1, 0).UTC(), state["worker2"][0].Running[0].LastWorked)
	assert.Equal(t, time.Unix(5, 0).UTC(), state["worker1"][0].Running[0].Created)
	assert.Equal(t, time.Unix(5, 0).UTC(), state["worker2"][0].Running[0].Created)

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
