package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	commandPtr := flag.String("c", "create", "command what to do")
	funcIdPtr := flag.String("fID", "", "ID of function")
	instIdPtr := flag.String("instID", "", "ID of instance")
	timeoutPtr := flag.Int("timeout", 30, "timeout in seconds")
	addressPtr := flag.String("addr", "localhost:50051", "server address")
	imageTagPtr := flag.String("im", "hyperfaas-hello:latest", "image Tag of function to be called")
	dataPtr := flag.String("d", "", "data to be passed to func")

	flag.Parse()

	//if len(os.Args) < 2 {
	//	fmt.Printf("Please specify an action:\n\n")
	//	fmt.Printf("%-35s %s\n", "create <image tag>", "create a function from image tag")
	//	fmt.Printf("%-35s %s\n", "start  <func ID>", "start instance of a function")
	//	fmt.Printf("%-35s %s\n", "call   <function id> <instance id>", "call a function")
	//	fmt.Printf("%-35s %s\n", "\nplease, run go run main.go again with arguments", "")
	//	return
	//}

	data := []byte(*dataPtr)

	switch *commandPtr {
	case "create":
		createFunc(*imageTagPtr, *timeoutPtr)
	case "start":
		startFunc(*funcIdPtr, *addressPtr, *timeoutPtr)
	case "call":
		callFunc(*funcIdPtr, *instIdPtr, data, *timeoutPtr)
	case "":
		fmt.Printf("")
	}

}

func startFunc(id string, address string, timeout int) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)
	funcID := commonpb.FunctionID{
		Id: id,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()
	instanceID, err := client.Start(ctx, &funcID)
	if err != nil {
		log.Fatalf("Error starting function: %v", err)
	}
	fmt.Printf("Started instance of function with instance id: %v\n", instanceID.Id)
}

func createFunc(imageTag string, timeout int) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v\n", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()
	createFuncResp, err := client.CreateFunction(ctx, createFuncReq)
	if err != nil {
		log.Fatalf("Error creating function: %v\n", err)
	}
	fmt.Printf("Created function from imageTag '%v'. Function id: %v\n", imageTag, createFuncResp.FunctionID)
}

func callFunc(funcID string, instanceID string, data []byte, timeout int) {
	fmt.Printf("Calling function ID %v at instance ID %v\n", funcID, instanceID)
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
		Data:       data,
		FunctionId: &function_id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	callResp, err := client.Call(ctx, &callReq)
	if err != nil {
		log.Fatalf("Error starting function: %v\n", err)
	}
	fmt.Printf("Function response: %v\n", string(callResp.Data))
}
