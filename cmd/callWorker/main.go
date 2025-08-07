package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	controllerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type functionState struct {
	fID     string
	running []instanceState
	idle    []instanceState
}

type instanceState struct {
	instID            string
	uptime            string
	timeSinceLastWork string
}

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
	client := controllerpb.NewControllerClient(conn)
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
	client := controllerpb.NewControllerClient(conn)

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
	client := controllerpb.NewControllerClient(conn)

	statusReq := &controllerpb.StatusRequest{
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
	client := controllerpb.NewControllerClient(conn)

	metricsReq := &controllerpb.MetricsRequest{
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

func getState(nodeId string, address string, timeout int) ([]functionState, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials())) //connect to worker
	if err != nil {
		fmt.Printf("Failed to connect: %v", err)
		return nil, err
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
		fmt.Printf("Error getting metrics: %v\n", err)
		return nil, err
	}
	fmt.Print("Successfully requested node state!")

	fStates := []functionState{}
	funcs := stateResp.Functions
	for _, funcState := range funcs {
		fmt.Println("Function ID:", funcState.FunctionId.Id)
		idle := []instanceState{}
		running := []instanceState{}
		fState := functionState{fID: funcState.FunctionId.Id, idle: idle, running: running}
		fmt.Println("\tRunning instances:")
		for _, instance := range funcState.Running {
			fmt.Printf("\t\t - instance ID:%v\tuptime:%v\ttime since last work:%v", instance.InstanceId, instance.Uptime, instance.TimeSinceLastWork)
			running = append(running, instanceState{instID: instance.InstanceId, uptime: fmt.Sprintf("%v", instance.Uptime), timeSinceLastWork: fmt.Sprintf("%v", instance.TimeSinceLastWork)})
		}

		fmt.Println("\t\tIdle instances:")
		for _, instance := range funcState.Idle {
			fmt.Printf("\t\t - instance ID:%v\tuptime:%v\ttime since last work:%v", instance.InstanceId, instance.Uptime, instance.TimeSinceLastWork)
			idle = append(idle, instanceState{instID: instance.InstanceId, uptime: fmt.Sprintf("%v", instance.Uptime), timeSinceLastWork: fmt.Sprintf("%v", instance.TimeSinceLastWork)})

		}

		fmt.Println("---------------------------------------------")
		fStates = append(fStates, fState)
	}
	return fStates, nil
}

func test(leafAddress string, workerAddress string, nodeID string, image_tag string, data []byte, timeout int) {
	fmt.Print("Testing callWorker.go\n")

	fmt.Print("Starting CreateAndCall-Test...")
	createAndCallSuccess := testCreateAndCall(leafAddress, workerAddress, image_tag, data, timeout)
	if createAndCallSuccess {
		fmt.Print("CreateAndCall-Test passed!")
	} else {
		fmt.Print("CreateAndCall-Test failed!")
	}

	fmt.Print("Starting StateAndMetrics-Test...")
	stateAndMetricsSuccess := testStateAndMetrics(workerAddress, nodeID, timeout)
	if stateAndMetricsSuccess {
		fmt.Print("StateAndMetrics-Test passed!")
	} else {
		fmt.Print("StateAndMetrics-Test failed!")
	}

	if createAndCallSuccess && stateAndMetricsSuccess {
		fmt.Print("Successfully passed all tests!e")
	}

}

func testCreateAndCall(leafAddress string, workerAddress string, image_tag string, data []byte, timeout int) bool {
	originalStdout := os.Stdout
	noErrors := true
	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs

	fID, err := createFunc(image_tag, leafAddress, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Creating function: failed; error: %v\nExiting CreateAndCall-Tests...\n", err)
		return false
	}
	fmt.Println("Creating function: passed")

	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
	instID, err := startFunc(fID, workerAddress, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Starting function: failed; error: %v\n", err)
		noErrors = false
	} else {
		fmt.Print("Starting function: passed\n")

		os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
		_, err := callFunc(fID, workerAddress, instID, data, timeout)
		os.Stdout = originalStdout
		if err != nil {
			fmt.Printf("Calling function: failed; error: %v\n", err)
			noErrors = false
		} else {
			fmt.Print("Calling function: passed\n")
		}

		os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
		_, err = stopInstance(instID, workerAddress, timeout)
		os.Stdout = originalStdout
		if err != nil {
			fmt.Printf("Stopping instance: failed; error: %v\n", err)
			noErrors = false
		} else {
			fmt.Print("Stopping instance: passed\n")
		}
	}

	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
	_, err = scheduleCall(fID, leafAddress, data, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Scheduling call: failed; error: %v\n", err)
		noErrors = false
	} else {
		fmt.Print("Scheduling call: passed\n")
	}

	return noErrors
}

func testStateAndMetrics(workerAddress string, nodeID string, timeout int) bool {
	originalStdout := os.Stdout
	noErrors := true

	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
	_, err := getStatusUpdate(nodeID, workerAddress, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Getting Status: failed; error: %v\n", err)
		noErrors = false
	} else {
		fmt.Print("Getting Status: passed\n")
	}

	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
	_, err = getMetricsUpdate(nodeID, workerAddress, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Getting Metrics: failed; error: %v\n", err)
		noErrors = false
	} else {
		fmt.Print("Getting Metrics: passed\n")
	}

	os.Stdout = os.NewFile(uintptr(0), "/dev/null") // Do not print functions outputs
	_, err = getState(nodeID, workerAddress, timeout)
	os.Stdout = originalStdout
	if err != nil {
		fmt.Printf("Getting State: failed; error: %v\n", err)
		noErrors = false
	} else {
		fmt.Print("Getting State: passed\n")
	}

	return noErrors
}
