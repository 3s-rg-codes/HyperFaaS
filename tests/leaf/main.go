package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

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

	req := &pb.ScheduleCallRequest{
		FunctionId: "hyperfaas-simul",
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
			testTwoCallsToSameFunction(client)
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

func testTwoCallsToSameFunction(client pb.LeafClient) {
	// sleep for random time between 100ms and 2 seconds
	time.Sleep(time.Duration(rand.Intn(1900)+100) * time.Millisecond)
	startReq := &pb.ScheduleCallRequest{
		FunctionId: "hyperfaas-simul",
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
