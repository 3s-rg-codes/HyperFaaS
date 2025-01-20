package main

import (
	"context"
	"fmt"
	"log"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Postman alternative xD...
// Now seriously, why is postman soooo slow?
func main() {
	// Create connection to leaf server
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create leaf client
	client := pb.NewLeafClient(conn)

	// Create request
	req := &pb.ScheduleCallRequest{
		FunctionId: "hyperfaas-hello",
		Data:       []byte("test data"),
	}

	// Call ScheduleCall RPC
	resp, err := client.ScheduleCall(context.Background(), req)
	if err != nil {
		log.Fatalf("Failed to schedule call: %v", err)
	}

	if resp.Error != nil {
		fmt.Printf("Error from function: %v\n", resp.Error)
	} else {
		fmt.Printf("Response data: %s\n", string(resp.Data))
	}
}
