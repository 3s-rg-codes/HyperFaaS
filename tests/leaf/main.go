package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
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
