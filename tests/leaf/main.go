package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	RequestedMemory    = 100 * 1024 * 1024 // 100MB
	RequestedCPUPeriod = 100000
	RequestedCPUQuota  = 50000
	SQLITE_DB_PATH     = "metrics.db"
	TIMEOUT            = 30 * time.Second
	DURATION           = 15 * time.Second
	RPS                = 15
)

type CallMetadata struct {
	CallQueuedTimestamp    string
	GotResponseTimestamp   string
	FunctionProcessingTime string
	InstanceID             string
}

func main() {
	// Create leaf client
	client, conn := createClient()
	defer conn.Close()

	imageTags := []string{
		"hyperfaas-hello:latest",
		"hyperfaas-echo:latest",
		"hyperfaas-simul:latest",
		"hyperfaas-thumbnailer:latest",
		"hyperfaas-bfs:latest",
	}

	functionIDs := make([]*common.FunctionID, len(imageTags))

	// Create functions and save their id:imagetag mapping
	for i, imageTag := range imageTags {
		functionID, err := createFunction(imageTag, &client)
		if err != nil {
			log.Fatalf("Failed to create function: %v", err)
		}
		functionIDs[i] = functionID
		/* err = saveFunctionId(functionID, imageTag)
		if err != nil {
			log.Fatalf("Failed to save function id: %v", err)
		} */
	}

	// Sequential calls
	//testSequentialCalls(client, functionIDs[0])

	// Concurrent calls for duration
	//testConcurrentCallsForDuration(client, functionIDs[0], RPS, DURATION)
	go testRampingCallsForDuration(client, functionIDs[0], RPS, DURATION, 60*time.Second)
	go testRampingCallsForDuration(client, functionIDs[1], RPS, DURATION, 60*time.Second)
	//go testRampingCallsForDuration(client, functionIDs[2], RPS, DURATION, 60*time.Second)
	time.Sleep(DURATION + 5*time.Second)
	// Send thumbnail request
	//sendThumbnailRequest(client, functiocallerServer := caller.NewCallerServer(config.Config.CallerServerAddress, logger, statsManager)nIDs[3])

	// Send BFS request
	//testBFS(client, functionIDs[4])

	// Test call with sleep
	testCallWithSleep(client, functionIDs[0], 20*time.Second)
}

func createClient() (pb.LeafClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return pb.NewLeafClient(conn), conn
}

func testConcurrentCalls(client pb.LeafClient, functionID *common.FunctionID, numCalls int) {
	// Create main context
	ctx := context.Background()

	g, _ := errgroup.WithContext(ctx)

	// Track success/failure counts
	var successCount, failureCount int32
	var totalLatency time.Duration
	var countMu sync.Mutex

	// Launch concurrent calls
	for i := 0; i < numCalls; i++ {
		g.Go(func() error {
			latency, err := sendCall(client, functionID)
			countMu.Lock()
			if err != nil {
				failureCount++
				fmt.Printf("Failed to send call: %v\n", err)
			} else {
				successCount++
				totalLatency += latency
				totalLatency += latency
			}
			countMu.Unlock()
			return nil // Don't propagate errors to cancel other goroutines
		})
	}

	// Wait for all goroutines to complete
	_ = g.Wait()
	if numCalls > 0 {
		avgLatency := totalLatency / time.Duration(numCalls)
		fmt.Printf("Concurrent calls complete - Successful: %d, Failed: %d, AvgLatency: %v\n", successCount, failureCount, avgLatency)
	} else {
		fmt.Printf("Concurrent calls complete - Successful: %d, Failed: %d, AvgLatency: %v\n", successCount, failureCount, 0)
	}
}

func testConcurrentCallsForDuration(client pb.LeafClient, functionID *common.FunctionID, rps int, duration time.Duration) {

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), duration+30*time.Second)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	var wg sync.WaitGroup
	for {
		select {
		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()
				testConcurrentCalls(client, functionID, rps)
			}()
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

func testRampingCallsForDuration(client pb.LeafClient, functionID *common.FunctionID, targetRPS int, duration time.Duration, rampUpTime time.Duration) {
	var wg sync.WaitGroup
	//seconds := int(duration.Seconds())

	rampUpSeconds := int(rampUpTime.Seconds())

	ctx, cancel := context.WithTimeout(context.Background(), duration+3*time.Second)
	defer cancel()

	currentRPS := targetRPS/rampUpSeconds + 1

	for i := 0; i < rampUpSeconds; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			if currentRPS > targetRPS {
				fmt.Printf("Rampup done\n")
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				testConcurrentCalls(client, functionID, currentRPS)
			}()
			time.Sleep(1 * time.Second)
			currentRPS += targetRPS / rampUpSeconds
		}
	}

	// Use a timeout on the WaitGroup
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("All calls completed successfully")
	case <-time.After(10 * time.Second):
		fmt.Println("Timed out waiting for all calls to complete")
	}
}

func sendCall(client pb.LeafClient, functionID *common.FunctionID) (time.Duration, error) {
	//time.Sleep(time.Duration(rand.Intn(10)+100) * time.Millisecond)
	startReq := &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       []byte(uuid.New().String()),
	}
	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	defer cancel()

	var metadata metadata.MD
	start := time.Now()
	_, err := client.ScheduleCall(ctx, startReq, grpc.Trailer(&metadata))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			//fmt.Printf("Timeout error: %v\n", ctx.Err())
			return 0, fmt.Errorf("timeout error: %v", ctx.Err())
		}
		//fmt.Printf("Failed to schedule call: %v\n", err)
		return 0, fmt.Errorf("failed to schedule call: %v", err)
	}

	// Uncomment if you want to test that the data is the same with the echo function
	/* if string(response.Data) != string(startReq.Data) {
		panic("data mismatch")
		return 0, fmt.Errorf("data mismatch: %v", response.Data)
	} else {
		fmt.Printf("Data matches: %v\n", response.Data)
	} */

	callMetadata := &CallMetadata{
		CallQueuedTimestamp:    metadata.Get("callQueuedTimestamp")[0],
		GotResponseTimestamp:   metadata.Get("gotResponseTimestamp")[0],
		FunctionProcessingTime: metadata.Get("functionProcessingTime")[0],
		InstanceID:             metadata.Get("instanceID")[0],
	}
	latency := time.Since(start)
	log.Printf("Latency: %v, Call queued at %s, got response at %s, function processing time: %s, instanceID: %s", latency, callMetadata.CallQueuedTimestamp, callMetadata.GotResponseTimestamp, callMetadata.FunctionProcessingTime, callMetadata.InstanceID)

	return latency, nil
}

func testSequentialCalls(client pb.LeafClient, functionID *common.FunctionID) {
	for i := 0; i < 100; i++ {
		req := &pb.ScheduleCallRequest{
			FunctionID: functionID,
			Data:       []byte(""),
		}

		_, err := client.ScheduleCall(context.Background(), req)
		if err != nil {
			log.Fatalf("Failed to schedule sequential call %d: %v", i, err)
		}
		fmt.Printf("Successfully got response from sequential call %d\n", i)
	}
}

func BuildMockClientHelper(controllerServerAddress string) (workerpb.ControllerClient, *grpc.ClientConn, error) {
	var err error
	connection, err := grpc.NewClient(controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := workerpb.NewControllerClient(connection)

	return testClient, connection, nil
}

func createFunction(imageTag string, client *pb.LeafClient) (*common.FunctionID, error) {
	createReq := &pb.CreateFunctionRequest{
		ImageTag: &common.ImageTag{Tag: imageTag},
		Config: &common.Config{
			Memory: RequestedMemory,
			Cpu: &common.CPUConfig{
				Period: RequestedCPUPeriod,
				Quota:  RequestedCPUQuota,
			},
			MaxConcurrency: 500,
			Timeout:        10,
		},
	}

	createFunctionResp, err := (*client).CreateFunction(context.Background(), createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create function: %v", err)
	}

	return createFunctionResp.FunctionID, nil
}

func saveFunctionId(functionID *common.FunctionID, imageTag string) error {
	db, err := sql.Open("sqlite3", SQLITE_DB_PATH)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("INSERT INTO function_images (function_id, image_tag) VALUES (?, ?)", functionID.Id, imageTag)
	if err != nil {
		return fmt.Errorf("failed to insert function id: %v", err)
	}
	return nil
}

func sendThumbnailRequest(client pb.LeafClient, functionID *common.FunctionID) (time.Duration, error) {
	resp, err := http.Get("https://picsum.photos/200/300")
	if err != nil {
		return 0, fmt.Errorf("failed to get image: %v", err)
	}
	defer resp.Body.Close()

	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read image bytes: %v", err)
	}

	// Save original image
	err = os.WriteFile("original_200x300.jpg", imageBytes, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to save original image: %v", err)
	}

	// Create input data matching InputData struct from thumbnailer
	input := struct {
		Image  []byte
		Width  int
		Height int
	}{
		Image:  imageBytes,
		Width:  100,
		Height: 100,
	}

	// Encode input data
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(input); err != nil {
		return 0, fmt.Errorf("failed to encode input: %v", err)
	}

	start := time.Now()
	r, err := client.ScheduleCall(context.Background(), &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       buf.Bytes(),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to schedule call: %v", err)
	}

	// Save resized image
	err = os.WriteFile("resized_100x100.jpg", r.Data, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to save resized image: %v", err)
	}

	return time.Since(start), nil
}

func testBFS(client pb.LeafClient, functionID *common.FunctionID) {
	input := struct {
		Size int
		Seed int
	}{
		Size: 1000,
		Seed: 100,
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(input); err != nil {
		log.Fatalf("failed to encode input: %v", err)
	}

	r, err := client.ScheduleCall(context.Background(), &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       buf.Bytes(),
	})
	if err != nil {
		log.Fatalf("failed to schedule call: %v", err)
	}
	// decode response
	type OutputData struct {
		Result      []int64
		Measurement struct {
			GraphGeneratingTime int64
			ComputeTime         int64
		}
	}

	var output OutputData
	if err := gob.NewDecoder(bytes.NewReader(r.Data)).Decode(&output); err != nil {
		log.Fatalf("failed to decode output: %v", err)
	}

	log.Printf("Received response from BFS: %v", output)
}

// this tests if the leaf is able to update its state to realise that a conatiner has timed out and it needs to scale up again.
func testCallWithSleep(client pb.LeafClient, functionID *common.FunctionID, sleep time.Duration) {
	_, err := sendCall(client, functionID)
	if err != nil {
		log.Fatalf("failed to send call: %v", err)
	}
	time.Sleep(sleep)
	_, err = sendCall(client, functionID)
	if err != nil {
		log.Fatalf("failed to send call: %v", err)
	}
}
