package caller

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer() *CallerServer {
	logger := slog.Default()
	statsManager := stats.NewStatsManager(logger, 30*time.Second)
	return NewCallerServer("localhost:0", logger, statsManager)
}

func TestRegisterUnregisterFunctionInstance(t *testing.T) {
	server := setupTestServer()

	instanceID := "test-instance-1"
	server.RegisterFunctionInstance(instanceID)

	assert.NotNil(t, server.GetInstanceCall(instanceID))
	assert.NotNil(t, server.GetInstanceResponse(instanceID))

	server.UnregisterFunctionInstance(instanceID)

	assert.Nil(t, server.GetInstanceCall(instanceID))
	assert.Nil(t, server.GetInstanceResponse(instanceID))
}

func TestQueueAndGetInstanceCall(t *testing.T) {
	server := setupTestServer()
	instanceID := "test-instance-2"
	server.RegisterFunctionInstance(instanceID)

	callData := []byte("test-call-data")
	server.QueueInstanceCall(instanceID, callData)

	callChan := server.GetInstanceCall(instanceID)
	require.NotNil(t, callChan)

	select {
	case receivedData := <-callChan:
		assert.Equal(t, callData, receivedData)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for call data")
	}

	server.UnregisterFunctionInstance(instanceID)
}

func TestQueueAndGetInstanceResponse(t *testing.T) {
	server := setupTestServer()
	instanceID := "test-instance-3"
	server.RegisterFunctionInstance(instanceID)

	responseData := []byte("test-response-data")
	server.QueueInstanceResponse(instanceID, responseData)

	responseChan := server.GetInstanceResponse(instanceID)
	require.NotNil(t, responseChan)

	select {
	case receivedData := <-responseChan:
		assert.Equal(t, responseData, receivedData)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for response data")
	}

	server.UnregisterFunctionInstance(instanceID)
}

func TestReady(t *testing.T) {
	server := setupTestServer()
	instanceID := "test-instance-4"
	functionID := "test-function-1"
	server.RegisterFunctionInstance(instanceID)

	callData := []byte("test-call-data")

	go func() {
		time.Sleep(10 * time.Millisecond)
		server.QueueInstanceCall(instanceID, callData)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	instID := &common.InstanceID{Id: instanceID}
	funcID := &common.FunctionID{Id: functionID}

	payload := &pb.Payload{
		InstanceId:     instID,
		FunctionId:     funcID,
		FirstExecution: true,
	}

	result, err := server.Ready(ctx, payload)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, callData, result.Data)
	assert.Equal(t, instanceID, result.InstanceId.Id)

	server.UnregisterFunctionInstance(instanceID)
}

func TestReadyTimeout(t *testing.T) {
	server := setupTestServer()
	instanceID := "test-instance-5"
	functionID := "test-function-2"
	server.RegisterFunctionInstance(instanceID)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	instID := &common.InstanceID{Id: instanceID}
	funcID := &common.FunctionID{Id: functionID}

	payload := &pb.Payload{
		InstanceId:     instID,
		FunctionId:     funcID,
		FirstExecution: true,
	}

	result, err := server.Ready(ctx, payload)
	assert.Nil(t, result)
	assert.Nil(t, err)

	assert.Nil(t, server.GetInstanceCall(instanceID))
	assert.Nil(t, server.GetInstanceResponse(instanceID))
}

func TestConcurrentOperations(t *testing.T) {
	server := setupTestServer()
	const concurrentInstances = 500
	const callsPerInstance = 10

	instanceIDs := make([]string, concurrentInstances)
	for i := 0; i < concurrentInstances; i++ {
		instanceIDs[i] = fmt.Sprintf("instance-%d", i)
		server.RegisterFunctionInstance(instanceIDs[i])
	}

	var mu sync.Mutex
	expectedResponses := make(map[string][][]byte)
	responsesReceived := make(map[string][][]byte)

	var wg sync.WaitGroup
	for _, instanceID := range instanceIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			calls := make([][]byte, callsPerInstance)
			responses := make([][]byte, callsPerInstance)
			for j := 0; j < callsPerInstance; j++ {
				calls[j] = []byte(fmt.Sprintf("call-%s-%d", id, j))
				responses[j] = []byte(fmt.Sprintf("response-%s-%d", id, j))
			}

			mu.Lock()
			expectedResponses[id] = responses
			responsesReceived[id] = make([][]byte, 0, callsPerInstance)
			mu.Unlock()

			// Set up goroutine that listens for responses
			respChan := server.GetInstanceResponse(id)
			require.NotNil(t, respChan)

			done := make(chan struct{})
			go func() {
				for {
					select {
					case resp := <-respChan:
						mu.Lock()
						responsesReceived[id] = append(responsesReceived[id], resp)
						mu.Unlock()
					case <-done:
						return
					}
				}
			}()

			// Simulate sending calls and having the system process and respond
			for j := 0; j < callsPerInstance; j++ {
				call := calls[j]
				response := responses[j]

				// Queue the call — this simulates the client sending a request
				server.QueueInstanceCall(id, call)

				// Simulate the instance being ready
				receivedCallChan := server.GetInstanceCall(id)
				require.NotNil(t, receivedCallChan)
				receivedCall := <-receivedCallChan
				require.Equal(t, call, receivedCall)

				// Simulate some processing delay
				time.Sleep(10 * time.Millisecond)

				// Queue the response — this should wake up the original caller
				server.QueueInstanceResponse(id, response)
			}

			time.Sleep(200 * time.Millisecond) // allow response delivery
			close(done)
		}(instanceID)
	}

	wg.Wait()

	// Check all expected responses were received
	for id, expected := range expectedResponses {
		mu.Lock()
		actual := responsesReceived[id]
		mu.Unlock()
		assert.Len(t, actual, len(expected), "Wrong number of responses for instance %s", id)
		for _, exp := range expected {
			found := false
			for _, act := range actual {
				if bytes.Equal(exp, act) {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected response %s not received for instance %s", exp, id)
		}
	}

	// Cleanup
	for _, id := range instanceIDs {
		server.UnregisterFunctionInstance(id)
	}
}

func TestQueueToUnregisteredInstance(t *testing.T) {
	server := setupTestServer()
	instanceID := "nonexistent-instance"

	server.QueueInstanceCall(instanceID, []byte("test-data"))
	server.QueueInstanceResponse(instanceID, []byte("test-data"))

	assert.Nil(t, server.GetInstanceCall(instanceID))
	assert.Nil(t, server.GetInstanceResponse(instanceID))
}

func TestReadyNonFirstExecution(t *testing.T) {
	server := setupTestServer()
	instanceID := "test-instance-6"
	functionID := "test-function-3"
	server.RegisterFunctionInstance(instanceID)

	responseData := []byte("previous-response")
	callData := []byte("new-call-data")

	go func() {
		time.Sleep(10 * time.Millisecond)
		server.QueueInstanceCall(instanceID, callData)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	instID := &common.InstanceID{Id: instanceID}
	funcID := &common.FunctionID{Id: functionID}

	payload := &pb.Payload{
		InstanceId:     instID,
		FunctionId:     funcID,
		FirstExecution: false,
		Data:           responseData,
	}

	result, err := server.Ready(ctx, payload)
	require.NoError(t, err)
	require.NotNil(t, result)

	responseChan := server.GetInstanceResponse(instanceID)
	select {
	case received := <-responseChan:
		assert.Equal(t, responseData, received)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Response was not queued")
	}

	server.UnregisterFunctionInstance(instanceID)
}

func TestConcurrentOperations2(t *testing.T) {
	server := setupTestServer()
	const concurrentInstances = 5000
	const callsPerInstance = 10

	// Create and register all instances first
	instanceIDs := make([]string, concurrentInstances)
	for i := 0; i < concurrentInstances; i++ {
		instanceIDs[i] = fmt.Sprintf("instance-%d", i)
		server.RegisterFunctionInstance(instanceIDs[i])
	}

	// Track expected calls and responses for verification
	expectedCalls := make(map[string][][]byte)
	expectedResponses := make(map[string][][]byte)
	var mu sync.Mutex

	// Collect received responses
	responsesReceived := make(map[string][][]byte)
	var wg sync.WaitGroup

	// For each instance, start a goroutine to send calls and check responses
	for i, instanceID := range instanceIDs {
		// Simulate instances
		wg.Add(1)
		go SimulateInstance(instanceID, server, &wg)

		wg.Add(1)
		go func(id string, index int) {
			defer wg.Done()

			// Prepare test data for this instance
			callsData := make([][]byte, callsPerInstance)
			responsesData := make([][]byte, callsPerInstance)
			for j := 0; j < callsPerInstance; j++ {
				callsData[j] = []byte(fmt.Sprintf("call-%s-%d", id, j))
				responsesData[j] = []byte{} // Helper returns empty []byte
			}

			// Track expected data
			mu.Lock()
			expectedCalls[id] = callsData
			expectedResponses[id] = responsesData
			responsesReceived[id] = make([][]byte, 0, callsPerInstance)
			mu.Unlock()

			// Get response channel for this instance
			responseChan := server.GetInstanceResponse(id)
			require.NotNil(t, responseChan)

			// Start goroutine to receive responses
			var responseWg sync.WaitGroup
			responseWg.Add(1)
			go func() {
				defer responseWg.Done()
				for j := 0; j < callsPerInstance; j++ {
					select {
					case resp := <-responseChan:
						mu.Lock()
						responsesReceived[id] = append(responsesReceived[id], resp)
						mu.Unlock()
					case <-time.After(10 * time.Second): // Increased timeout for processing delay
						t.Errorf("Timeout waiting for response %d for instance %s", j, id)
						return
					}
				}
			}()

			// Queue calls for this instance
			for _, callData := range callsData {
				// Small random delay to simulate concurrent queueing
				time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)
				server.QueueInstanceCall(id, callData)
			}

			// Wait for all responses to be received
			responseWg.Wait()
		}(instanceID, i)
	}

	// Wait for all test operations to complete
	wg.Wait()

	// Verify all expected responses were received
	for id, expected := range expectedResponses {
		received, ok := responsesReceived[id]
		if !assert.True(t, ok, "No responses received for instance %s", id) {
			continue
		}

		// Check we received the correct number of responses
		assert.Equal(t, len(expected), len(received), "Wrong number of responses for instance %s", id)

		// All responses should be empty []byte
		for _, rec := range received {
			assert.Empty(t, rec, "Expected empty response for instance %s", id)
		}
	}

	// Cleanup
	for _, id := range instanceIDs {
		server.UnregisterFunctionInstance(id)
	}
}

func SimulateInstance(instanceID string, server *CallerServer, wg *sync.WaitGroup) {
	defer wg.Done()
	//Get call channel
	callChan := server.GetInstanceCall(instanceID)
	for {
		select {
		case _ = <-callChan:
			// Simulate some processing delay
			time.Sleep(10 * time.Millisecond)

			// Queue the response
			server.QueueInstanceResponse(instanceID, make([]byte, 0))
		case <-time.After(10 * time.Second):
			return
		}
	}
}
