package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Please specify an action:\n\n")
		fmt.Printf("%-35s %s\n", "create <image tag>", "create a function from image tag")
		fmt.Printf("%-35s %s\n", "start  <func ID>", "start instance of a function")
		fmt.Printf("%-35s %s\n", "call   <function id> <instance id>", "call a function")
		fmt.Printf("%-35s %s\n", "\nplease, run go run main.go again with arguments", "")
		return
	}

	switch os.Args[1] {
	case "create":
		createFunc(os.Args[2])
	case "start":
		startFunc(os.Args[2])
	case "call":
		callFunc(os.Args[2], os.Args[3])
	}

}

func startFunc(id string) {
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)
	funcID := commonpb.FunctionID{
		Id: id,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	instanceID, err := client.Start(ctx, &funcID)
	if err != nil {
		log.Fatalf("Error starting function: %v", err)
	}
	fmt.Printf("Started instance of function with instance id: %v\n", instanceID.Id)
}

func createFunc(imageTag string) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := leafpb.NewLeafClient(conn)
	cpuConfig := &commonpb.CPUConfig{
		Period: 100000,
		Quota:  50000,
	}
	config := &commonpb.Config{
		Memory: 67108864,
		Cpu:    cpuConfig,
	}
	image_tag := &commonpb.ImageTag{
		Tag: imageTag,
	}
	createFuncReq := &leafpb.CreateFunctionRequest{
		ImageTag: image_tag,
		Config:   config,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	createFuncResp, err := client.CreateFunction(ctx, createFuncReq)
	if err != nil {
		log.Fatalf("Error creating function: %v", err)
	}
	fmt.Printf("Created function from imageTag '%v'. Function id: %v\n", imageTag, createFuncResp.FunctionID)
}

func callFunc(funcID string, instanceID string) {
	fmt.Printf("Calling function ID %v at instance ID %v", funcID, instanceID)
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)

	function_id := commonpb.FunctionID{
		Id: funcID,
	}

	instance_id := commonpb.InstanceID{
		Id: instanceID,
	}

	callReq := commonpb.CallRequest{
		InstanceId: &instance_id,
		Data:       []byte{},
		FunctionId: &function_id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	callResp, err := client.Call(ctx, &callReq)
	if err != nil {
		log.Fatalf("Error starting function: %v", err)
	}
	fmt.Printf("Function response: %v\n", string(callResp.Data))
}
