package main

import (
	"context"
	"io"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	//"gotest.tools/v3/assert"
)

type statsTest struct {
	testName            string
	disconnects         bool
	reconnects          bool
	nodeIDs             []string
	controllerWorkloads []controllerWorkload
	timeout             time.Duration
}

var workloadImageTags = []string{"hello:latest", "echo:latest"}

// Tests that stats are correctly streamed to all listening nodes. Nodes may disconnect and reconnect.
// During that disconnect, the stats should not be lost, but should be cached and sent to the node when it reconnects.
// In the end the stats received by each node should be equal to the expected stats.
// The expected stats are determined by the doWorkload function  based on the controllerWorkloads array.
func TestStats(t *testing.T) {
	//setup()
	//defer teardown()

	controllerWorkloads := []controllerWorkload{
		{
			testName:          "normal execution of hello image",
			ImageTag:          workloadImageTags[0],
			ExpectedError:     false,
			ExpectsResponse:   true,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "",
		},
	}

	statsTests := []statsTest{
		{
			testName:            "streaming stats to one connected node",
			disconnects:         true,
			reconnects:          false,
			nodeIDs:             []string{"1"},
			controllerWorkloads: controllerWorkloads,
			timeout:             12 * time.Second,
		},
		/*
				{
					testName:    "streaming stats to three connected nodes",
					disconnects: false,
					reconnects:  false,
					nodeIDs:     []string{"1", "2", "3"},
					controllerWorkloads
			:   controllerWorkloads
			,
					timeout:     12 * time.Second,
				},

					{
						testName:    "one node disconnects while streaming stats",
						disconnects: true,
						reconnects:  false,
						nodeIDs:     []string{"1"},
						controllerWorkloads
				:   controllerWorkloads
				,
					},
					{
						testName:    "one node disconnects and reconnects while streaming stats",
						disconnects: true,
						reconnects:  true,
						nodeIDs:     []string{"1"},
						controllerWorkloads
				:   controllerWorkloads
				,
					},
		*/
	}

	for _, statsTestCase := range statsTests {
		t.Run(statsTestCase.testName, func(t *testing.T) {

			//stopSignals is a map that stores the stop signals for each node
			stopSignals := make(map[string]chan bool)

			//recievedStatsPerNode is a map that stores the stats recieved by each node
			recievedStatsPerNode := make(map[string][]*stats.StatusUpdate)
			mu := sync.Mutex{}

			go func() {
				//sleep for a random amount of time ( 4 - 6 seconds)
				time.Sleep(time.Duration(rand.Intn(2)+4) * time.Second)

				//send a stop signal to each node
				if statsTestCase.disconnects {
					for _, nodeID := range statsTestCase.nodeIDs {
						stopSignals[nodeID] <- true
					}
				}
			}()

			statsChan := make(chan *[]*stats.StatusUpdate)
			// Do Workload concurrently
			wgNodes := sync.WaitGroup{}
			wgWorkload := sync.WaitGroup{}

			wgWorkload.Add(1)
			go func(wg1 *sync.WaitGroup) {
				expectedStats := doWorkload(t, statsTestCase)
				log.Debug().Msg("Workload is done")
				wg1.Done()
				statsChan <- expectedStats

			}(&wgWorkload)

			// We run one goroutine for each node
			log.Debug().Msgf("RUNNING WITH %d NODES", len(statsTestCase.nodeIDs))
			for _, nodeID := range statsTestCase.nodeIDs {

				log.Debug().Msgf("Adding 1 to the WaitGroupCounter: %s", nodeID)

				wgNodes.Add(1)

				go func(nodeID string, wg1 *sync.WaitGroup, mutex *sync.Mutex) {
					client, connection := BuildMockClient(t)
					defer wg1.Done()
					defer log.Debug().Msgf("Removing 1 from the WaitGroupCounter: %s", nodeID)

					defer connection.Close()

					recievedStatusUpdates := []*stats.StatusUpdate{}

					//After the node has listened to the stream, we add the stats to the map
					defer func() {
						mutex.Lock()
						recievedStatsPerNode[nodeID] = recievedStatusUpdates
						mutex.Unlock()
					}()

					ctx, cancel := context.WithTimeout(context.Background(), statsTestCase.timeout)
					defer cancel()

					s, err := client.Status(ctx, &pb.StatusRequest{NodeID: nodeID})
					if err != nil {
						log.Error().Msgf("Error (Node: %s): %v", nodeID, err)
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
								log.Info().Msgf("EOF (Node: %s)", nodeID)
								return
							}
							if err != nil {
								log.Error().Msgf("Error: %v", err)
								return
							}

							// Copy the stats to a new struct to avoid copying mutex
							statCopy := &stats.StatusUpdate{
								InstanceID: stat.InstanceId,
								Type:       stat.Type,
								Event:      stat.Event,
								Status:     stat.Status,
							}

							recievedStatusUpdates = append(recievedStatusUpdates, statCopy)

							//log.Debug().Msgf("State of stats (Node: %s): %v", nodeID, recievedStats)
						}

						if ctx.Err() != nil {
							break
						}
					}
				}(nodeID, &wgNodes, &mu)
			}
			//time.Sleep(1 * time.Second)
			log.Debug().Msg("Waiting for all goroutines to finish")
			wgWorkload.Wait()
			expected := <-statsChan
			wgNodes.Wait()

			// We check if the stats recieved by each node contains all of the stats that were expected
			for _, nodeID := range statsTestCase.nodeIDs {

				actual := recievedStatsPerNode[nodeID]
				// First, check if the lengths of the arrays are equal
				if len(*expected) != len(actual) {
					t.Error("lengths of expected and actual arrays are not equal")
				}

				// Create a map to count occurrences of each StatusUpdate in the expected array
				expectedCount := make(map[stats.StatusUpdate]int)
				for _, e := range *expected {
					expectedCount[*e]++
				}

				// Create a map to count occurrences of each StatusUpdate in the actual array
				actualCount := make(map[stats.StatusUpdate]int)
				for _, a := range actual {
					actualCount[*a]++
				}
				result := reflect.DeepEqual(expectedCount, actualCount)

				assert.True(t, result, "The stats recieved by node %s are not equal to the expected stats", nodeID)
			}
		})

	}

}

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
func doWorkload(t *testing.T, statsTestCase statsTest) *[]*stats.StatusUpdate {

	client, connection := BuildMockClient(t)

	statusUpdates := []*stats.StatusUpdate{}

	// Wait for all of the nodes to be connected to the stream
	time.Sleep(4 * time.Second)

	for _, testCase := range statsTestCase.controllerWorkloads {

		testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{}})

		// Depending on the test case, the container may not start
		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "start", Status: "error"})
		} else {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "start", Status: "success"})
		}

		response, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: testContainerID, Params: &pb.Params{Data: testCase.CallPayload}})

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "call", Status: "error"})
		} else {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "call", Status: "success"})
		}
		//If there was a response, there is a container response event
		if testCase.ExpectsResponse && response != nil {
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: testContainerID.Id, Type: "container", Event: "response", Status: "success"})
		}

		responseContainerID, err := client.Stop(context.Background(), testContainerID)

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "error"})
		} else if responseContainerID != nil && responseContainerID.Id == testContainerID.Id {
			// add a success event to the stats
			statusUpdates = append(statusUpdates, &stats.StatusUpdate{InstanceID: responseContainerID.Id, Type: "container", Event: "stop", Status: "success"})
		}
	}

	t.Cleanup(func() {
		connection.Close()
	})
	return &statusUpdates
}

// Using assert.EqualExportedValues , this function checks if two arrays of structs have the same number of elements, and that these elements have the same values.
// The order of the elements does not matter.
func EqualStatusUpdates(t *testing.T, expected, actual []*stats.StatusUpdate) bool {

	if len(expected) != len(actual) {
		return false
	}

	t.Helper()

	for i := range expected {
		assert.EqualExportedValues(t, expected[i], actual[i])
	}
	return true
}
