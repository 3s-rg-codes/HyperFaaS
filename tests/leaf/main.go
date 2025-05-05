package main

import (
	"context"
	"fmt"
	"log"
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
)

func main() {
	// Create leaf client
	client, conn := createClient()
	defer conn.Close()

	imageTags := []string{
		"hyperfaas-hello:latest",
		"hyperfaas-echo:latest",
		"hyperfaas-simul:latest",
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
	//testSequentialCalls(client, createFunctionResp.FunctionID)

	// Concurrent calls for duration
	testConcurrentCallsForDuration(client, functionIDs[0], 60, 60*time.Second)
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
func testConcurrentCallsForDuration(client pb.LeafClient, functionID *common.FunctionID, rps int, duration time.Duration) {
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-ticker.C:
				go testConcurrentCalls(client, functionID, rps)
			case <-done:
				return
			}
		}
	}()
	time.Sleep(duration)
	ticker.Stop()
	done <- true

}

func sendCall(client pb.LeafClient, functionID *common.FunctionID) (time.Duration, error) {
	//time.Sleep(time.Duration(rand.Intn(10)+100) * time.Millisecond)
	startReq := &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       []byte(""),
	}
	//ctx := context.WithValue(context.Background(), "RequestID", uuid.New().String())
	start := time.Now()
	_, err := client.ScheduleCall(context.Background(), startReq)
	if err != nil {
		fmt.Printf("Failed to schedule call: %v\n", err)
		return 0, fmt.Errorf("failed to schedule call: %v", err)
	}

	return time.Since(start), nil
}

func testSequentialCalls(client pb.LeafClient, functionID *common.FunctionID) {
	for i := 0; i < 20; i++ {
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
		},
	}

	createFunctionResp, err := (*client).CreateFunction(context.Background(), createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create function: %v", err)
	}

	return createFunctionResp.FunctionID, nil
}
