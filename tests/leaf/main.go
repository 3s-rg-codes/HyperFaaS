package main

import (
	"bytes"
	"context"
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
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	RequestedMemory    = 100 * 1024 * 1024 // 100MB
	RequestedCPUPeriod = 100000
	RequestedCPUQuota  = 50000
	SQLITE_DB_PATH     = "metrics.db"
	TIMEOUT            = 10 * time.Second
	DURATION           = 60 * time.Second
	RPS                = 1500
)

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
	}

	//Concurrent calls
	//testConcurrentCalls(client, functionIDs[0], 10)
	// Sequential calls
	//testSequentialCalls(client, functionIDs[0])

	// Concurrent calls for duration
	//testConcurrentCallsForDuration(client, functionIDs[0], RPS, DURATION)
	testRampingCallsForDuration(client, functionIDs[0], RPS, DURATION, 60*time.Second)

	// Send thumbnail request
	//sendThumbnailRequest(client, functionIDs[3])

	// Send BFS request
	//testBFS(client, functionIDs[4])
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
			}
			countMu.Unlock()
			return nil // Don't propagate errors to cancel other goroutines
		})
	}

	// Wait for all goroutines to complete
	_ = g.Wait()

	avgLatency := totalLatency / time.Duration(numCalls)

	fmt.Printf("Concurrent calls complete - Successful: %d, Failed: %d, AvgLatency: %v\n", successCount, failureCount, avgLatency)
}

func testConcurrentCallsForDurationOLD(client pb.LeafClient, functionID *common.FunctionID, rps int, duration time.Duration) {
	var wg sync.WaitGroup
	seconds := int(duration.Seconds())

	for i := 0; i < seconds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			testConcurrentCalls(client, functionID, rps)
		}()
		time.Sleep(1 * time.Second)
	}

	time.Sleep(2 * time.Second)

	wg.Wait()
}

func testConcurrentCallsForDuration(client pb.LeafClient, functionID *common.FunctionID, rps int, duration time.Duration) {
	var wg sync.WaitGroup
	seconds := int(duration.Seconds())

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), duration+3*time.Second)
	defer cancel()

	for i := 0; i < seconds; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			wg.Add(1)
			go func() {
				defer wg.Done()
				testConcurrentCalls(client, functionID, rps)
			}()
			time.Sleep(1 * time.Second)
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

func testRampingCallsForDuration(client pb.LeafClient, functionID *common.FunctionID, targetRPS int, duration time.Duration, rampUpTime time.Duration) {
	var wg sync.WaitGroup
	//seconds := int(duration.Seconds())

	rampUpSeconds := int(rampUpTime.Seconds())

	ctx, cancel := context.WithTimeout(context.Background(), duration+3*time.Second)
	defer cancel()

	currentRPS := targetRPS / rampUpSeconds

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
		Data:       []byte(""),
	}
	//ctx := context.WithValue(context.Background(), "RequestID", uuid.New().String())
	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	defer cancel()
	start := time.Now()
	_, err := client.ScheduleCall(ctx, startReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Printf("Timeout error: %v\n", ctx.Err())
			return 0, fmt.Errorf("timeout error: %v", ctx.Err())
		}
		fmt.Printf("Failed to schedule call: %v\n", err)
		return 0, fmt.Errorf("failed to schedule call: %v", err)
	}

	return time.Since(start), nil
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
