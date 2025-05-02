package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pbc "github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	leafClient := pb.NewLeafClient(conn)

	createReq := &pb.CreateFunctionRequest{
		ImageTag: &common.ImageTag{Tag: "hyperfaas-hello:latest"},
		Config: &common.Config{
			Memory: 100 * 1024 * 1024, // 100MB
			Cpu: &common.CPUConfig{
				Period: 100000, // 100ms in microseconds
				Quota:  50000,
			},
		},
	}

	createFunctionResp, err := leafClient.CreateFunction(context.Background(), createReq)
	if err != nil {
		log.Fatalf("Failed to create function: %v", err)
	}

	fmt.Printf("Successfully created function: %v\n", createFunctionResp.FunctionID)

	req := &pb.ScheduleCallRequest{
		FunctionID: createFunctionResp.FunctionID,
		Data:       []byte(""),
	}

	_, err = leafClient.ScheduleCall(context.Background(), req)
	if err != nil {
		log.Fatalf("Failed to schedule call: %v", err)
	}

	fmt.Printf("Successfully got response\n")

	// Create leaf client
	client, conn := createClient()
	defer conn.Close()

	//Concurrent calls
	//testConcurrentCalls(client, createFunctionResp.FunctionID, 20, createFunctionResp.FunctionID)
	// Sequential calls
	testSequentialCalls(client, createFunctionResp.FunctionID)

	// Test worker concurrent calls
	controllerClient, conn, err := BuildMockClientHelper("localhost:50051")
	if err != nil {
		log.Fatalf("Failed to build mock client: %v", err)
	}
	defer conn.Close()

	TestWorkerConcurrentCalls(controllerClient, createFunctionResp.FunctionID)
}

func createClient() (pb.LeafClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return pb.NewLeafClient(conn), conn
}

func testConcurrentCalls(client pb.LeafClient, functionID *common.FunctionID, numCalls int, functionId *common.FunctionID) {
	// Create main context
	ctx := context.Background()

	g, _ := errgroup.WithContext(ctx)

	// Track success/failure counts
	var successCount, failureCount int32
	var countMu sync.Mutex

	// Launch concurrent calls
	for i := 0; i < numCalls; i++ {
		g.Go(func() error {
			err := sendCall(client, functionID)
			countMu.Lock()
			if err != nil {
				failureCount++
				fmt.Printf("Failed to send call: %v\n", err)
			} else {
				successCount++
			}
			countMu.Unlock()
			return nil // Don't propagate errors to cancel other goroutines
		})
	}

	// Wait for all goroutines to complete
	_ = g.Wait()

	fmt.Printf("Concurrent calls complete - Successful: %d, Failed: %d\n", successCount, failureCount)
}

func sendCall(client pb.LeafClient, functionID *common.FunctionID) error {
	// sleep for random time between 100ms and 2 seconds
	time.Sleep(time.Duration(rand.Intn(1900)+100) * time.Millisecond)
	startReq := &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       []byte(""),
	}

	_, err := client.ScheduleCall(context.Background(), startReq)
	if err != nil {
		return fmt.Errorf("failed to schedule call: %v", err)
	}

	return nil
}

func testSequentialCalls(client pb.LeafClient, functionID *common.FunctionID) {
	for i := 0; i < 20; i++ {
		//time.Sleep(time.Duration(rand.Intn(1900)+100) * time.Millisecond)
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

func TestWorkerConcurrentCalls(client workerpb.ControllerClient, functionID *common.FunctionID) error {
	totalInstances := 500
	totalFunctions := 10
	totalCallsPerInstance := 200

	instanceIDs := make([]string, totalInstances)
	functions := make([]string, totalFunctions)
	for i := 0; i < totalFunctions; i++ {
		functions[i] = functionID.Id
	}

	// Start all instances
	for i := 0; i < totalInstances; i++ {
		instanceID, err := client.Start(context.Background(), &pbc.FunctionID{Id: functions[rand.Intn(totalFunctions)]})
		if err != nil {
			log.Printf("Error starting instance: %v", err)
			return fmt.Errorf("error starting instance: %v", err)
		}
		instanceIDs[i] = instanceID.Id
	}
	log.Printf("Started %d instances", totalInstances)
	// Send all calls
	ctx := context.Background()

	g, _ := errgroup.WithContext(ctx)
	var successCount, failureCount int32
	var countMu sync.Mutex

	for _, instanceID := range instanceIDs {
		for i := 0; i < totalCallsPerInstance; i++ {
			g.Go(func() error {
				response, err := client.Call(context.Background(), &pbc.CallRequest{InstanceId: &pbc.InstanceID{Id: instanceID}, Data: make([]byte, 0), FunctionId: &pbc.FunctionID{Id: functions[rand.Intn(totalFunctions)]}})
				if err != nil {
					countMu.Lock()
					failureCount++
					countMu.Unlock()
					log.Printf("Error calling instance: %v", err)
					return fmt.Errorf("error calling instance: %v", err)
				}
				if response == nil {
					countMu.Lock()
					failureCount++
					countMu.Unlock()
					log.Printf("No response from instance: %v", instanceID)
					return fmt.Errorf("no response from instance: %v", instanceID)
				}
				countMu.Lock()
				successCount++
				countMu.Unlock()
				return nil
			})
		}
	}

	// Wait for all goroutines to complete
	_ = g.Wait()

	fmt.Printf("Concurrent calls complete - Successful: %d, Failed: %d\n", successCount, failureCount)

	return nil
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
