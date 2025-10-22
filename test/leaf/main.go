// nolint
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

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	RequestedMemory    = 100 * 1024 * 1024 // 100MB
	RequestedCPUPeriod = 100000
	RequestedCPUQuota  = 50000
	SQLITE_DB_PATH     = "metrics.db"
	TIMEOUT            = 30 * time.Second
	DURATION           = 120 * time.Second
	RPS                = 2500
	ETCD_ADDRESS       = "localhost:2379"
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

	functionIDs := make([]string, len(imageTags))

	// Create functions and save their id:imagetag mapping
	for i, imageTag := range imageTags {
		functionID, err := createFunction(imageTag)
		if err != nil {
			log.Fatalf("Failed to create function: %v", err)
		}
		log.Printf("Created function: %v", functionID)
		functionIDs[i] = functionID
	}

	// Sequential calls
	testSequentialCalls(client, functionIDs[0])

	// Concurrent calls for duration
	// testConcurrentCallsForDuration(client, functionIDs[0], RPS, DURATION)
	go testRampingCallsForDuration(client, functionIDs[0], RPS, DURATION, 60*time.Second)
	go testRampingCallsForDuration(client, functionIDs[1], RPS, DURATION, 60*time.Second)
	go testRampingCallsForDuration(client, functionIDs[2], RPS, DURATION, 60*time.Second)
	time.Sleep(DURATION + 5*time.Second)
	// Send thumbnail request
	//sendThumbnailRequest(client, functiocallerServer := caller.NewCallerServer(config.Config.CallerServerAddress, logger, statsManager)nIDs[3])

	// Send BFS request
	//testBFS(client, functionIDs[4])

	// Test call with sleep
	fmt.Printf("Testing call after sleeping\n")
	testCallWithSleep(client, functionIDs[0], 20*time.Second)
}

func createClient() (leafpb.LeafClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return leafpb.NewLeafClient(conn), conn
}

func testConcurrentCalls(client leafpb.LeafClient, functionID string, numCalls int) {
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

func testConcurrentCallsForDuration(client leafpb.LeafClient, functionID string, rps int, duration time.Duration) {

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

func testRampingCallsForDuration(client leafpb.LeafClient, functionID string, targetRPS int, duration time.Duration, rampUpTime time.Duration) {
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

func sendCall(client leafpb.LeafClient, functionID string) (time.Duration, error) {
	startReq := &common.CallRequest{
		FunctionId: functionID,
		Data:       []byte(""),
	}

	deadline := time.Now().Add(TIMEOUT)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
		start := time.Now()
		_, err := client.ScheduleCall(ctx, startReq)
		cancel()
		if err != nil {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.NotFound && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if ctx.Err() == context.DeadlineExceeded {
				return 0, fmt.Errorf("timeout error: %v", ctx.Err())
			}
			return 0, fmt.Errorf("failed to schedule call: %v", err)
		}
		return time.Since(start), nil
	}
}

func testSequentialCalls(client leafpb.LeafClient, functionID string) {
	for i := 0; i < 100; i++ {
		req := &common.CallRequest{
			FunctionId: functionID,
			Data:       []byte(""),
		}

		_, err := client.ScheduleCall(context.Background(), req)
		if err != nil {
			log.Fatalf("Failed to schedule sequential call %d: %v", i, err)
		}
		fmt.Printf("Successfully got response from sequential call %d\n", i)
	}
}

func BuildMockClientHelper(controllerServerAddress string) (workerpb.WorkerClient, *grpc.ClientConn, error) {
	var err error
	connection, err := grpc.NewClient(controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := workerpb.NewWorkerClient(connection)

	return testClient, connection, nil
}

func createFunction(imageTag string) (string, error) {
	etcdAddr := os.Getenv("ETCD_ADDRESS")
	if etcdAddr == "" {
		etcdAddr = ETCD_ADDRESS
	}

	client, err := metadata.NewClient([]string{etcdAddr}, metadata.Options{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create metadata client: %w", err)
	}
	defer client.Close()

	createReq := &common.CreateFunctionRequest{
		Image: &common.Image{Tag: imageTag},
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := client.PutFunction(ctx, createReq)
	if err != nil {
		return "", fmt.Errorf("failed to store metadata: %w", err)
	}

	return id, nil
}

func sendThumbnailRequest(client leafpb.LeafClient, functionID string) (time.Duration, error) {
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
	r, err := client.ScheduleCall(context.Background(), &common.CallRequest{
		FunctionId: functionID,
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

func testBFS(client leafpb.LeafClient, functionID string) {
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

	r, err := client.ScheduleCall(context.Background(), &common.CallRequest{
		FunctionId: functionID,
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
func testCallWithSleep(client leafpb.LeafClient, functionID string, sleep time.Duration) {
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
