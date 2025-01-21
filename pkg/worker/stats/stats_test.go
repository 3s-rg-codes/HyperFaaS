package stats

import (
	"context"
	"flag"
	"github.com/3s-rg-codes/HyperFaaS/helpers"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	//"gotest.tools/v3/assert"
)

type statsTest struct {
	testName            string
	disconnects         bool
	reconnects          bool
	nodeIDs             []string
	controllerWorkloads []helpers.ControllerWorkload
	timeout             time.Duration
}

var (
	controllerServerAddress = flag.String("ServerAdress", helpers.SERVER_ADDRESS, "specify controller server adress")
	CPUPeriod               = flag.Int64("cpuPeriod", 100000, "CPU period")
	CPUQuota                = flag.Int64("cpuQuota", 50000, "CPU quota")
	MemoryLimit             = (*flag.Int64("memoryLimit", 250000000, "Memory limit in MB")) * 1024 * 1024
	environment             = flag.String("environment", helpers.ENVIRONMENT, "specify environment to run")
)

var workloadImageTags = []string{"hyperfaas-hello:latest", "hyperfaas-echo:latest"}

/*
	 This function performs some workload on the server, starting, calling and stopping containers
	 It then returns the expected stats each node should receive after the workload
	 Examples of a stats event:
		{
	    "instance_id": "b7bb229b0c625938726ee299fefc2246929b393d3041d511cdd416dcbb64a615",
	    "type": "container",
	    "event": "start",
	    "status": ""
		}

		{
	    "instance_id": "b7bb229b0c625938726ee299fefc2246929b393d3041d511cdd416dcbb64a615",
	    "type": "container",
	    "event": "call",
	    "status": "success"
		}
*/
func doWorkload(t *testing.T, statsTestCase statsTest) *[]*StatusUpdate {

	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	statusUpdates := []*StatusUpdate{}

	// Wait for all of the nodes to be connected to the stream
	time.Sleep(4 * time.Second)

	for _, testCase := range statsTestCase.controllerWorkloads {

		testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: *CPUPeriod, Quota: *CPUQuota}, Memory: MemoryLimit}})

		// Depending on the test case, the container may not start
		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "start", Status: "error"})
		} else {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "start", Status: "success"})
		}

		response, err := client.Call(context.Background(), &common.CallRequest{InstanceId: testContainerID, Data: testCase.CallPayload})

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "call", Status: "error"})
		} else {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "call", Status: "success"})
		}
		//If there was a response, there is a container response event
		if testCase.ExpectsResponse && response != nil {
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "response", Status: "success"})
		}

		responseContainerID, err := client.Stop(context.Background(), testContainerID)

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "error"})
		} else if responseContainerID != nil && responseContainerID.Id == testContainerID.Id {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "success"})
		}

		//UNCOMMENT THIS TO INTENTIONALLY FAIL THE TEST
		//statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: "break", Event: "break", Status: "break"})

	}

	t.Cleanup(func() {
		connection.Close()
	})
	return &statusUpdates
}

// Using assert.EqualExportedValues , this function checks if two arrays of structs have the same number of elements, and that these elements have the same values.
// The order of the elements does not matter.
func EqualStatusUpdates(t *testing.T, expected, actual []*StatusUpdate) bool {

	if len(expected) != len(actual) {
		return false
	}

	t.Helper()

	for i := range expected {
		assert.EqualExportedValues(t, expected[i], actual[i])
	}
	return true
}

// Tests that a node can reconnect to the StatusUpdate stream after disconecting.
func TestStatusReconnection(t *testing.T) {

	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func(wg1 *sync.WaitGroup) {
		defer wg1.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		node1, err := client.Status(ctx, &pb.StatusRequest{NodeID: "1"})

		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, err := node1.Recv()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Logf("Error: %v", err)
					return
				}

			}

			if err != nil {
				t.Logf("Error: %v", err)
				return
			}

		}
	}(&wg)

	wg.Wait()

	wg.Add(1)
	go func(wg1 *sync.WaitGroup) {
		defer wg1.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		node1, err := client.Status(ctx, &pb.StatusRequest{NodeID: "1"})
		if err != nil {
			t.Logf("Error: %v", err)
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, err := node1.Recv()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Logf("Error: %v", err)
					return
				}

			}

			if err != nil {
				t.Logf("Error: %v", err)
				return
			}

		}
	}(&wg)

	wg.Wait()

	t.Cleanup(func() {
		connection.Close()
	})
}

func ConnectNode(t *testing.T, nodeID string, wg1 *sync.WaitGroup, mutex *sync.Mutex, recievedStatsPerNode map[string][]*StatusUpdate, stopSignals map[string]chan bool, timeout time.Duration) {

	client, connection, err := helpers.BuildMockClient(*controllerServerAddress)
	if err != nil {
		t.Errorf("Error creating the mock client: %v", err)
	}

	defer connection.Close()
	defer wg1.Done()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s, err := client.Status(ctx, &pb.StatusRequest{NodeID: nodeID})
	if err != nil {
		t.Logf("Error (Node: %s): %v", nodeID, err)
	}

	for {
		select {
		case <-stopSignals[nodeID]:
			t.Logf("Stop signal Received (Node: %s)", nodeID)
			return
		case <-ctx.Done():
			t.Logf("Context Done (Node: %s)", nodeID)
			return
		default:
			stat, err := s.Recv()
			if err == io.EOF {
				t.Logf("EOF (Node: %s)", nodeID)
				return
			}
			if err != nil {
				t.Logf("Error: %v", err)
				return
			}

			// Copy the stats to a new struct to avoid copying mutex
			statCopy := &StatusUpdate{
				InstanceID: stat.InstanceId,
				Type:       stat.Type,
				Event:      stat.Event,
				Status:     stat.Status,
			}

			mutex.Lock()
			recievedStatsPerNode[nodeID] = append(recievedStatsPerNode[nodeID], statCopy)
			mutex.Unlock()

		}

		if ctx.Err() != nil {
			break
		}
	}
}

// Tests that stats are correctly streamed to all listening nodes. Nodes may disconnect and reconnect.
// During that disconnect, the stats should not be lost, but should be cached and sent to the node when it reconnects.
// In the end the stats received by each node should be equal to the expected stats.
// The expected stats are determined by the doWorkload function  based on the controllerWorkloads array.

func TestStats(t *testing.T) {

	controllerWorkloads := []helpers.ControllerWorkload{
		{
			TestName:          "normal execution of hello image",
			ImageTag:          workloadImageTags[0],
			ExpectedError:     false,
			ExpectsResponse:   true,
			ExpectedErrorCode: codes.OK,
			CallPayload:       []byte(""),
		},
	}

	statsTests := []statsTest{
		{
			testName:            "streaming stats to one connected node",
			disconnects:         false,
			reconnects:          false,
			nodeIDs:             []string{"1"},
			controllerWorkloads: controllerWorkloads,
			timeout:             12 * time.Second,
		},

		{
			testName:            "streaming stats to three connected nodes",
			disconnects:         false,
			reconnects:          false,
			nodeIDs:             []string{"10", "20", "30"},
			controllerWorkloads: controllerWorkloads,
			timeout:             12 * time.Second,
		},

		{
			testName:            "one node disconnects and reconnects while streaming stats",
			disconnects:         true,
			reconnects:          true,
			nodeIDs:             []string{"16"},
			controllerWorkloads: controllerWorkloads,
			timeout:             12 * time.Second,
		},
	}

	for _, statsTestCase := range statsTests {
		t.Run(statsTestCase.testName, func(t *testing.T) {
			// To keep track of the workload
			wgWorkload := sync.WaitGroup{}
			statsChan := make(chan *[]*StatusUpdate)

			wgWorkload.Add(1)
			go func(wg1 *sync.WaitGroup) {
				expectedStats := doWorkload(t, statsTestCase)
				t.Log("Workload is done")
				wg1.Done()
				statsChan <- expectedStats

			}(&wgWorkload)
			//stopSignals is a map that stores the stop signals for each node
			stopSignals := make(map[string]chan bool)

			if statsTestCase.disconnects {
				go func() {
					for _, nodeID := range statsTestCase.nodeIDs {
						stopSignals[nodeID] = make(chan bool)
						t.Logf("Node %s will disconnect", nodeID)
						time.Sleep(3 * time.Second)
						stopSignals[nodeID] <- true
					}
				}()
			}

			//recievedStatsPerNode is a map that stores the stats recieved by each node
			recievedStatsPerNode := make(map[string][]*StatusUpdate)

			wgNodes := sync.WaitGroup{}
			mu := sync.Mutex{}

			for _, nodeID := range statsTestCase.nodeIDs {
				wgNodes.Add(1)
				go ConnectNode(t, nodeID, &wgNodes, &mu, recievedStatsPerNode, stopSignals, statsTestCase.timeout)
			}

			wgWorkload.Wait()
			expected := <-statsChan
			wgNodes.Wait()

			if statsTestCase.reconnects {
				for _, nodeID := range statsTestCase.nodeIDs {
					wgNodes.Add(1)
					go ConnectNode(t, nodeID, &wgNodes, &mu, recievedStatsPerNode, stopSignals, statsTestCase.timeout)
				}
			}

			wgNodes.Wait()

			// We check if the stats recieved by each node contains all of the stats that were expected
			for _, nodeID := range statsTestCase.nodeIDs {

				actual := recievedStatsPerNode[nodeID]

				if len(*expected) != len(actual) {
					t.Errorf("lengths of expected and actual arrays are not equal: \n expected: %d !=  actual: %d", len(*expected), len(actual))
				}

				//  count occurrences of each StatusUpdate in the expected array
				expectedCount := make(map[StatusUpdate]int)
				for _, e := range *expected {
					expectedCount[*e]++
				}

				//  count occurrences of each StatusUpdate in the actual array
				actualCount := make(map[StatusUpdate]int)
				for _, a := range actual {
					actualCount[*a]++
				}

				// compare
				result := reflect.DeepEqual(expectedCount, actualCount)

				assert.True(t, result, "The stats recieved by node %s are not equal to the expected stats", nodeID)

				helpers.TestController.StatsManager.RemoveListener(nodeID)
			}
		})
	}
}
