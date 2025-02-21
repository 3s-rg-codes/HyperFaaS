package helpers

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type ResourceSpec struct {
	CPUPeriod   int64
	CPUQuota    int64
	MemoryLimit int64
}

type ControllerWorkload struct {
	TestName          string     `json:"workload_name"`
	ImageTag          string     `json:"image_tag"`
	ExpectsError      bool       `json:"expects_error"`
	ReturnError       bool       `json:"return_error"`
	ExpectsResponse   bool       `json:"expects_response"`
	ExpectedResponse  []byte     `json:"expected_response"`
	ErrorCode         codes.Code `json:"error_code"`
	ExpectedErrorCode codes.Code `json:"expected_error_code"`
	CallPayload       []byte     `json:"call_payload"`
	InstanceID        string     `json:"instance_id"`
}

func BuildMockClientHelper(controllerServerAddress string) (pb.ControllerClient, *grpc.ClientConn, error) {
	var err error
	connection, err := grpc.NewClient(controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)

	return testClient, connection, nil
}

func DoWorkloadHelper(client pb.ControllerClient, logger slog.Logger, spec ResourceSpec, testCase ControllerWorkload) (*[]*stats.StatusUpdate, error) {

	cID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: spec.CPUPeriod, Quota: spec.CPUQuota}, Memory: spec.MemoryLimit}})
	if err != nil {
		return nil, err
	}

	logger.Debug("Started container", "container", cID)

	var statusUpdates []*stats.StatusUpdate

	if testCase.ExpectsError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: stats.TypeContainer, Event: stats.EventStart, Status: stats.StatusFailed})
	} else {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: stats.TypeContainer, Event: stats.EventStart, Status: stats.StatusSuccess})
	}

	response, err := client.Call(context.Background(), &common.CallRequest{InstanceId: cID, Data: testCase.CallPayload})
	if err != nil {
		return nil, err
	}
	logger.Debug("Called container", "response", response.Data)

	if testCase.ExpectsError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: stats.TypeContainer, Event: stats.EventCall, Status: stats.StatusFailed})
	} else {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: stats.TypeContainer, Event: stats.EventCall, Status: stats.StatusSuccess})
	}
	//If there was a response, there is a container response event
	if testCase.ExpectsResponse && response != nil {
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: cID.Id, Type: stats.TypeContainer, Event: stats.EventResponse, Status: stats.StatusSuccess})
	}

	responseContainerID, err := client.Stop(context.Background(), cID)
	if err != nil {
		return nil, err
	}
	logger.Debug("Stopped container", "container", responseContainerID)

	if testCase.ExpectsError {
		// add an error event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: stats.TypeContainer, Event: stats.EventStop, Status: stats.StatusFailed})
	} else if responseContainerID != nil && responseContainerID.Id == cID.Id {
		// add a success event to the stats
		statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: stats.TypeContainer, Event: stats.EventStop, Status: stats.StatusSuccess})
	}

	//UNCOMMENT THIS TO INTENTIONALLY FAIL THE TEST
	//statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: responseContainerID.Id, Type: "break", Event: "break", Status: "break"})
	return &statusUpdates, nil
}

func ConnectNodeHelper(controllerServerAddress string, nodeID string, logger slog.Logger, wg *sync.WaitGroup, stopSignal chan bool, timeout time.Duration) ([]*stats.StatusUpdate, error) {

	client, conn, err := BuildMockClientHelper(controllerServerAddress)
	if err != nil {
		logger.Error("Error creating client", "error", err.Error())
	}
	logger.Debug("Created listener node as client", "nodeID", nodeID)

	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			logger.Error("Error occurred closing connection", "error", err.Error())
		}
	}(conn)

	var receivedStats []*stats.StatusUpdate
	defer wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s, err := client.Status(ctx, &pb.StatusRequest{NodeID: nodeID})
	if err != nil {
		return nil, err
	}

	for {
		select {
		case <-stopSignal:
			return receivedStats, fmt.Errorf("stop signal received")

		case <-ctx.Done():
			return receivedStats, fmt.Errorf("context hit timeout")

		default:
			stat, err := s.Recv()
			if status.Code(err) == codes.DeadlineExceeded { //This will happen when the call finishes and we try to reach the node
				logger.Info("Deadline exceeded", "nodeId", nodeID)
				return receivedStats, nil
			}
			if err != nil {
				return receivedStats, fmt.Errorf("error: %v", err)
			}

			logger.Debug("Received stat", "stat", stat, "nodeID", nodeID)
			// Copy the stats to a new struct to avoid copying mutex
			statCopy := &stats.StatusUpdate{
				InstanceID: stat.InstanceId,
				Type:       stats.UpdateType(stat.Type),
				Event:      stats.UpdateEvent(stat.Event),
				Status:     stats.UpdateStatus(stat.Status),
			}

			receivedStats = append(receivedStats, statCopy)
		}

		if ctx.Err() != nil {
			logger.Error("context error", "error", ctx.Err())
			break
		}
	}
	return receivedStats, nil
}

func Evaluate(actual []*stats.StatusUpdate, expected []*stats.StatusUpdate) (bool, error) {

	if len(expected) != len(actual) {
		return false, fmt.Errorf("unequal length of expected (%v) and actual (%v) list", len(expected), len(actual))
	}

	//  count occurrences of each StatusUpdate in the expected array
	expectedCount := make(map[stats.StatusUpdate]int)
	for _, e := range expected {
		expectedCount[*e]++
	}

	//  count occurrences of each StatusUpdate in the actual array
	actualCount := make(map[stats.StatusUpdate]int)
	for _, a := range actual {
		actualCount[*a]++
	}

	// compare
	result := reflect.DeepEqual(expectedCount, actualCount)

	return result, nil
}
