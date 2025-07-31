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
	addressLPtr := flag.String("addrL", "localhost:50051", "leaf address")
	addressWPtr := flag.String("addrW", "localhost:50050", "worker address")
	imageTagPtr := flag.String("im", "hyperfaas-hello:latest", "image Tag of function to be called")
	dataPtr := flag.String("d", "", "data to be passed to func")
	nodeIdPtr := flag.String("node", "0000", "node ID for status requests") //<-- what default value should be used?

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
		createFunc(*imageTagPtr, *addressLPtr, *timeoutPtr)
	case "schedule_call":
		scheduleCall(*funcIdPtr, *addressLPtr, data, *timeoutPtr)
	case "start":
		startFunc(*funcIdPtr, *addressWPtr, *timeoutPtr)
	case "call":
		callFunc(*funcIdPtr, *addressWPtr, *instIdPtr, data, *timeoutPtr)
	case "stop":
		stopInstance(*instIdPtr, *addressWPtr, *timeoutPtr)
	case "status":
		getStatusUpdate(*nodeIdPtr, *addressWPtr, *timeoutPtr)
	case "metrics":
		getMetricsUpdate(*nodeIdPtr, *addressWPtr, *timeoutPtr)
	case "state":
		getState(*nodeIdPtr, *addressWPtr, *timeoutPtr)
	case "":
		fmt.Printf("")
	}

}

func createFunc(imageTag string, address string, timeout int) { //calls Leaf
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	funcReq := &leafpb.CreateFunctionRequest{
		ImageTag: image_tag,
		Config:   config,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	funcResp, err := client.CreateFunction(ctx, funcReq)
	if err != nil {
		log.Fatalf("Error creating function: %v\n", err)
	}
	fmt.Printf("Created function from imageTag '%v'. Function id: %v\n", imageTag, funcResp.FunctionID)
}

func scheduleCall(id string, address string, data []byte, timeout int) { //calls leaf
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := leafpb.NewLeafClient(conn)

	funcID := &commonpb.FunctionID{
		Id: id,
	}

	scheduleCallReq := &leafpb.ScheduleCallRequest{
		FunctionID: funcID,
		Data:       data,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	scheduleCallResponse, err := client.ScheduleCall(ctx, scheduleCallReq)
	if err != nil {
		log.Fatalf("Error scheduling call: %v", err)
	}
	if scheduleCallResponse.Error != nil {
		log.Fatalf("Internal error scheduling call: %v\n", scheduleCallResponse.Error)
	}
	fmt.Printf("Scheduled call! Received response: %v\n", string(scheduleCallResponse.Data))

}

func startFunc(id string, address string, timeout int) { //calls worker
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

func callFunc(funcID string, address string, instanceID string, data []byte, timeout int) { //calls Worker
	fmt.Printf("Calling function ID %v at instance ID %v\n", funcID, instanceID)
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
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

func stopInstance(id string, address string, timeout int) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)

	instID := &commonpb.InstanceID{
		Id: id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	respInstId, err := client.Stop(ctx, instID)
	if err != nil {
		log.Fatalf("Error stopping instance: %v\n", err)
	}
	fmt.Printf("Successfully stopped instance. Worker response: %v\n", respInstId.Id)
}

func getStatusUpdate(nodeId string, address string, timeout int) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)

	statusReq := &controllerpb.StatusRequest{
		NodeID: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	statusResp, err := client.Status(ctx, statusReq)
	if err != nil {
		log.Fatalf("Error getting status: %v\n", err)
	}
	fmt.Printf("Successfully requested status update. Worker response: %v\n", statusResp) //<-- not sure how to display response (stream)
}

func getMetricsUpdate(nodeId string, address string, timeout int) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)

	metricsReq := &controllerpb.MetricsRequest{
		NodeID: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	metricsResp, err := client.Metrics(ctx, metricsReq)
	if err != nil {
		log.Fatalf("Error getting metrics: %v\n", err)
	}
	fmt.Printf("Successfully requested metrics update\nCPU percentages: [%v]\nRAM usage: %v%%\n", metricsResp.CpuPercentPercpu, metricsResp.UsedRamPercent)
}

func getState(nodeId string, address string, timeout int) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	client := controllerpb.NewControllerClient(conn)

	stateReq := &controllerpb.StateRequest{
		NodeId: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	stateResp, err := client.State(ctx, stateReq)
	if err != nil {
		log.Fatalf("Error getting metrics: %v\n", err)
	}
	fmt.Print("Successfully requested node state!")

	funcs := stateResp.Functions
	for _, funcState := range funcs {
		fmt.Println("Function ID:", funcState.FunctionId)

		fmt.Println("\tRunning instances:")
		for _, instance := range funcState.Running {
			fmt.Println("\t\t - ", instance)
		}

		fmt.Println("\t\tIdle instances:")
		for _, instance := range funcState.Idle {
			fmt.Println("\t\t - ", instance)
		}

		fmt.Println("---------------------------------------------")
	}
}
