package main

import (
	"context"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	//"gotest.tools/v3/assert"
)

type statsTest struct {
	testName    string
	disconnects bool
	reconnects  bool
	nodeIDs     []string
	testCases   []testCase
	timeout     time.Duration
}

var imageTags2 = []string{"luccadibe/hyperfaas-functions:hello", "luccadibe/hyperfaas-functions:echo"}

//Somewow using wg.Wait() is not working as expected,  IT BLOCKS forever
//Just waiting 15 seconds does work
// I have no idea why
// The goroutines do finish
// If you try to add an additional waitgroup.Done() it will panic
// What?

func TestStats(t *testing.T) {
	//setup()
	//defer teardown()

	testCases := []testCase{
		{
			testName:          "normal execution of hello image",
			ImageTag:          imageTags2[0],
			ExpectedError:     false,
			ExpectsResponse:   true,
			ExpectedErrorCode: codes.OK,
			CallPayload:       "",
		},
	}

	statsTests := []statsTest{
		{
			testName:    "streaming stats to one connected node",
			disconnects: true,
			reconnects:  false,
			nodeIDs:     []string{"1"},
			testCases:   testCases,
			timeout:     12 * time.Second,
		},
		{
			testName:    "streaming stats to three connected nodes",
			disconnects: false,
			reconnects:  false,
			nodeIDs:     []string{"1", "2", "3"},
			testCases:   testCases,
			timeout:     12 * time.Second,
		},
		/*
			{
				testName:    "one node disconnects while streaming stats",
				disconnects: true,
				reconnects:  false,
				nodeIDs:     []string{"1"},
				testCases:   testCases,
			},
			{
				testName:    "one node disconnects and reconnects while streaming stats",
				disconnects: true,
				reconnects:  true,
				nodeIDs:     []string{"1"},
				testCases:   testCases,
			},
		*/
	}

	var wg sync.WaitGroup

	for _, statsTestCase := range statsTests {
		t.Run(statsTestCase.testName, func(t *testing.T) {

			//stopSignals is a map that stores the stop signals for each node
			stopSignals := make(map[string]chan bool)

			//recievedStatsPerNode is a map that stores the stats recieved by each node
			recievedStatsPerNode := make(map[string][]pb.StatusUpdate)
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

			statsChan := make(chan *[]pb.StatusUpdate)
			// Do Workload concurrently
			wg.Add(1)
			go func(wg1 *sync.WaitGroup) {
				defer wg1.Done()
				expectedStats := doWorkload(t, statsTestCase)
				log.Debug().Msg("DONE WORKLOAD")
				statsChan <- expectedStats
			}(&wg)

			// We run one goroutine for each node
			log.Debug().Msgf("%d", len(statsTestCase.nodeIDs))
			for _, nodeID := range statsTestCase.nodeIDs {

				log.Debug().Msgf("Adding 1 to the WaitGroupCounter: %s", nodeID)

				wg.Add(1)
				go func(nodeID string, wg1 *sync.WaitGroup) {
					client, connection := BuildMockClient(t)
					defer log.Debug().Msgf("Removing 1 from the WaitGroupCounter: %s", nodeID)
					defer wg1.Done()
					defer connection.Close()

					recievedStats := []pb.StatusUpdate{}

					//After the node has listened to the stream, we add the stats to the map
					defer func() {
						mu.Lock()
						recievedStatsPerNode[nodeID] = recievedStats
						mu.Unlock()
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
								log.Debug().Msg("RETURNING ")
								return
							}

							// Copy the stats to a new struct to avoid copying mutex
							statCopy := &pb.StatusUpdate{
								InstanceId: stat.InstanceId,
								Type:       stat.Type,
								Event:      stat.Event,
								Status:     stat.Status,
							}

							recievedStats = append(recievedStats, *statCopy)

							//log.Debug().Msgf("State of stats (Node: %s): %v", nodeID, recievedStats)
						}

						if ctx.Err() != nil {
							break
						}
					}
				}(nodeID, &wg)
			}
			//time.Sleep(1 * time.Second)
			log.Debug().Msg("Waiting for all goroutines to finish")
			wg.Wait()
			log.Debug().Msg("AAAAAAAAAAAAAAAA")
			expectedStats := <-statsChan

			// We check if the stats recieved by each node contains all of the stats that were expected
			for _, nodeID := range statsTestCase.nodeIDs {

				rS := recievedStatsPerNode[nodeID]

				nodeRecievedAllStats := containsAll(rS, *expectedStats)

				/*
					if !t {
						log.Error().Msgf("Stats not equal (Node: %s)", nodeID)
						log.Error().Msgf("Expected: %v", *expectedStats)
						log.Error().Msgf("Recieved: %v", rS)
					}*/
				//
				assert.True(t, nodeRecievedAllStats, "Stats not equal (Node: %s)", nodeID)
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
func doWorkload(t *testing.T, statsTestCase statsTest) *[]pb.StatusUpdate {

	client, connection := BuildMockClient(t)

	stats := []pb.StatusUpdate{}

	// Wait for all of the nodes to be connected to the stream
	time.Sleep(4 * time.Second)

	for _, testCase := range statsTestCase.testCases {

		testContainerID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: testCase.ImageTag}, Config: &pb.Config{}})

		// Depending on the test case, the container may not start
		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: testContainerID.Id, Type: "container", Event: "start", Status: "error"})
		} else {
			// add a success event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: testContainerID.Id, Type: "container", Event: "start", Status: "success"})
		}

		response, err := client.Call(context.Background(), &pb.CallRequest{InstanceId: testContainerID, Params: &pb.Params{Data: testCase.CallPayload}})

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: testContainerID.Id, Type: "container", Event: "call", Status: "error"})
		} else {
			// add a success event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: testContainerID.Id, Type: "container", Event: "call", Status: "success"})
		}
		//If there was a response, there is a container response event
		if testCase.ExpectsResponse && response != nil {
			stats = append(stats, pb.StatusUpdate{InstanceId: testContainerID.Id, Type: "container", Event: "response", Status: "success"})
		}

		responseContainerID, err := client.Stop(context.Background(), testContainerID)

		if testCase.ExpectedError && err != nil {
			// add an error event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: responseContainerID.Id, Type: "container", Event: "stop", Status: "error"})
		} else if responseContainerID != nil && responseContainerID.Id == testContainerID.Id {
			// add a success event to the stats
			stats = append(stats, pb.StatusUpdate{InstanceId: responseContainerID.Id, Type: "container", Event: "stop", Status: "success"})
		}

	}

	t.Cleanup(func() {
		connection.Close()
	})
	return &stats
}

func containsAll(haystack, needle []pb.StatusUpdate) bool {
	for _, n := range needle {
		found := false
		for _, h := range haystack {
			if h.InstanceId == n.InstanceId && h.Type == n.Type && h.Event == n.Event && h.Status == n.Status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
