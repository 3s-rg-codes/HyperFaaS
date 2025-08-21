package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	commandPtr := flag.String("c", "create", "command what to do")
	funcIdPtr := flag.String("fID", "", "ID of function")
	instIdPtr := flag.String("instID", "", "ID of instance")
	timeoutPtr := flag.Int("timeout", 30, "timeout in seconds")
	addressLPtr := flag.String("addrL", "localhost:50050", "leaf address")
	addressWPtr := flag.String("addrW", "localhost:50051", "worker address")
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
	case "test":
		test(*addressLPtr, *addressWPtr, *nodeIdPtr, *imageTagPtr, data, *timeoutPtr)
	default:
		fmt.Printf("Unrecognised command '%v'\n", *commandPtr)
	}

}

func createFunc(imageTag string, address string, timeout int) (string, error) { //calls Leaf
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return "", err
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
		fmt.Printf("Error creating function: %v\n", err)
		return "", err
	}
	fmt.Printf("Created function from imageTag '%v'. Function id: %v\n", imageTag, funcResp.FunctionID.Id)
	return fmt.Sprintf("%v", funcResp.FunctionID.Id), nil
}

func scheduleCall(id string, address string, data []byte, timeout int) (string, error) { //calls leaf
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return "", err
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
		fmt.Printf("Error scheduling call: %v", err)
		return "", err
	}
	if scheduleCallResponse.Error != nil {
		fmt.Printf("Internal error scheduling call: %v\n", scheduleCallResponse.Error)
		return "", errors.New(fmt.Sprintf("%v", scheduleCallResponse.Error))
	}
	fmt.Printf("Scheduled call! Received response: %v\n", string(scheduleCallResponse.Data))
	return fmt.Sprintf("%v", scheduleCallResponse.Data), nil
}

func startFunc(id string, address string, timeout int) (string, error) { //calls worker
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return "", err
	}
	defer conn.Close()
	client := workerpb.NewControllerClient(conn)
	funcID := commonpb.FunctionID{
		Id: id,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()
	instanceID, err := client.Start(ctx, &funcID)
	if err != nil {
		fmt.Printf("Error starting function: %v", err)
		return "", err
	}
	fmt.Printf("Started instance of function with instance id: %v\n", instanceID.Id)
	return fmt.Sprintf("%v", instanceID.Id), nil
}

func callFunc(funcID string, address string, instanceID string, data []byte, timeout int) (string, error) { //calls Worker
	fmt.Printf("Calling function ID %v at instance ID %v\n", funcID, instanceID)
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return "", err
	}
	defer conn.Close()
	client := workerpb.NewControllerClient(conn)

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
		fmt.Printf("Error starting function: %v\n", err)
		return "", err
	}
	fmt.Printf("Function response: %v\n", string(callResp.Data))
	return string(callResp.Data), nil
}

func stopInstance(id string, address string, timeout int) (string, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return "", err
	}
	defer conn.Close()
	client := workerpb.NewControllerClient(conn)

	instID := &commonpb.InstanceID{
		Id: id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	respInstId, err := client.Stop(ctx, instID)
	if err != nil {
		fmt.Printf("Error stopping instance: %v\n", err)
		return "", err
	}
	fmt.Printf("Successfully stopped instance. Worker response: %v\n", respInstId.Id)
	return fmt.Sprintf("%v", respInstId.Id), nil
}

func getStatusUpdate(nodeId string, address string, timeout int) (string, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return "", nil
	}
	defer conn.Close()
	client := workerpb.NewControllerClient(conn)

	statusReq := &workerpb.StatusRequest{
		NodeID: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	statusResp, err := client.Status(ctx, statusReq)
	if err != nil {
		fmt.Printf("Error getting status: %v\n", err)
		return "", nil
	}
	fmt.Printf("Successfully requested status update. Worker response: %#v\n", statusResp) //<-- not sure how to display response (stream)
	return fmt.Sprintf("%v", statusResp), nil
}

func getMetricsUpdate(nodeId string, address string, timeout int) ([]string, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return nil, err
	}
	defer conn.Close()
	client := workerpb.NewControllerClient(conn)

	metricsReq := &workerpb.MetricsRequest{
		NodeID: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	metricsResp, err := client.Metrics(ctx, metricsReq)
	if err != nil {
		fmt.Printf("Error getting metrics: %v\n", err)
		return nil, err
	}
	fmt.Printf("Successfully requested metrics update\nCPU percentages: [%v]\nRAM usage: %v%%\n", metricsResp.CpuPercentPercpu, metricsResp.UsedRamPercent)
	return []string{fmt.Sprintf("%v", metricsResp.CpuPercentPercpu), fmt.Sprintf("%v", metricsResp.UsedRamPercent)}, nil
}

func test(leafAddress string, workerAddress string, nodeID string, image_tag string, data []byte, timeout int) {
	fmt.Print(">Testing callWorker.go\n\n")

	fmt.Print("\n>Starting CreateAndCall-Test...\n\n")
	createAndCallSuccess := testCreateAndCall(leafAddress, workerAddress, image_tag, data, timeout)
	if createAndCallSuccess {
		fmt.Print(">CreateAndCall-Test passed!\n\n")
	} else {
		fmt.Print(">CreateAndCall-Test failed!\n\n")
	}

	fmt.Print(">Starting StateAndMetrics-Test...\n")
	stateAndMetricsSuccess := testStateAndMetrics(workerAddress, nodeID, timeout)
	if stateAndMetricsSuccess {
		fmt.Print(">StateAndMetrics-Test passed!\n\n")
	} else {
		fmt.Print(">StateAndMetrics-Test failed!\n\n")
	}

	if createAndCallSuccess && stateAndMetricsSuccess {
		fmt.Print(">Successfully passed all tests!\n")
	}

}

func testCreateAndCall(leafAddress string, workerAddress string, image_tag string, data []byte, timeout int) bool {
	noErrors := true

	fID, err := createFunc(image_tag, leafAddress, timeout)
	if err != nil {
		fmt.Printf("\n>Creating function: failed; error: %v\nExiting CreateAndCall-Tests...\n\n\n", err)
		return false
	}
	fmt.Print("\n>Creating function: passed\n\n\n")

	instID, err := startFunc(fID, workerAddress, timeout)
	if err != nil {
		fmt.Printf("\n>Starting function: failed; error: %v\n\n\n", err)
		noErrors = false
	} else {
		fmt.Print("\n>Starting function: passed\n\n\n")

		_, err := callFunc(fID, workerAddress, instID, data, timeout)
		if err != nil {
			fmt.Printf("\n>Calling function: failed; error: %v\n\n\n", err)
			noErrors = false
		} else {
			fmt.Print("\n>Calling function: passed\n\n\n")
		}

		_, err = stopInstance(instID, workerAddress, timeout)
		if err != nil {
			fmt.Printf("\n>Stopping instance: failed; error: %v\n\n\n", err)
			noErrors = false
		} else {
			fmt.Print("\n>Stopping instance: passed\n\n\n")
		}
	}

	_, err = scheduleCall(fID, leafAddress, data, timeout)
	if err != nil {
		fmt.Printf("\n>Scheduling call: failed; error: %v\n\n\n", err)
		noErrors = false
	} else {
		fmt.Print("\n>Scheduling call: passed\n\n\n")
	}

	return noErrors
}

func testStateAndMetrics(workerAddress string, nodeID string, timeout int) bool {
	noErrors := true

	_, err := getStatusUpdate(nodeID, workerAddress, timeout)
	if err != nil {
		fmt.Printf("\n>Getting Status: failed; error: %v\n\n\n", err)
		noErrors = false
	} else {
		fmt.Print("\n>Getting Status: passed\n\n\n")
	}

	_, err = getMetricsUpdate(nodeID, workerAddress, timeout)
	if err != nil {
		fmt.Printf("\n>Getting Metrics: failed; error: %v\n\n\n", err)
		noErrors = false
	} else {
		fmt.Print("\n>Getting Metrics: passed\n\n\n")
	}

	_, err = getState(nodeID, workerAddress, timeout)
	if err != nil {
		fmt.Printf("\n>Getting State: failed; error: %v\n\n\n", err)
		noErrors = false
	} else {
		fmt.Print("\n>Getting State: passed\n\n\n")
	}

	return noErrors
}
