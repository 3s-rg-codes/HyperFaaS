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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Postman alternative xD...
// Now seriously, why is postman soooo slow?
func main() {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	leafClient := pb.NewLeafClient(conn)

	createReq := &pb.CreateFunctionRequest{
		ImageTag: &common.ImageTag{Tag: "hyperfaas-simul"},
		Config: &common.Config{
			Memory: 100 * 1024 * 1024, // 100MB
			Cpu: &common.CPUConfig{
				Period: 100000, // 100ms in microseconds - Docker's default period
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
	// Call ScheduleCall RPC
	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			testTwoCallsToSameFunction(client, createFunctionResp.FunctionID)
		}()
	}
	wg.Wait()

}

func createClient() (pb.LeafClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return pb.NewLeafClient(conn), conn
}

func testTwoCallsToSameFunction(client pb.LeafClient, functionID *common.FunctionID) {
	// sleep for random time between 100ms and 2 seconds
	time.Sleep(time.Duration(rand.Intn(1900)+100) * time.Millisecond)
	startReq := &pb.ScheduleCallRequest{
		FunctionID: functionID,
		Data:       []byte(""),
	}

	_, err := client.ScheduleCall(context.Background(), startReq)
	if err != nil {
		log.Fatalf("Failed to schedule call: %v", err)
	}

	/* req := &pb.ScheduleCallRequest{
		FunctionId: "hyperfaas-hello",
		Data:       []byte(""),
	}

	resp, err = client.ScheduleCall(context.Background(), req)
	if err != nil {
		log.Fatalf("Failed to schedule second call: %v", err)
	} */

	fmt.Printf("Successfully got response\n")
}
