package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pbc "github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	RequestedMemory       = 100 * 1024 * 1024 // 100MB
	RequestedCPUPeriod    = 100000
	RequestedCPUQuota     = 50000
	SQLITE_DB_PATH        = "./benchmarks/metrics.db"
	totalInstances        = 5
	totalCallsPerInstance = 500
)

// This test sends concurrent calls directly to the worker.
// It must first register the functions through the leaf node so their metadata is available in the db
func main() {
	// Create leaf client
	leafClient, leafConn := createLeafClient()
	defer leafConn.Close()

	imageTags := []string{
		"hyperfaas-hello:latest",
		"hyperfaas-echo:latest",
		"hyperfaas-simul:latest",
	}

	functionIDs := make([]*common.FunctionID, len(imageTags))

	// Create functions and save their id:imagetag mapping
	for i, imageTag := range imageTags {
		functionID, err := createFunction(imageTag, &leafClient)
		if err != nil {
			log.Fatalf("Failed to create function: %v", err)
		}
		err = saveFunctionId(functionID, imageTag)
		if err != nil {
			log.Fatalf("Failed to save function id: %v", err)
		}
		functionIDs[i] = functionID
	}

	ctx := context.Background()

	g, _ := errgroup.WithContext(ctx)

	for _, functionID := range functionIDs {
		g.Go(func() error {
			return TestWorkerConcurrentCalls(functionID)
		})
	}

	g.Wait()

}

func createWorkerClient() (workerpb.ControllerClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return workerpb.NewControllerClient(conn), conn
}

func createLeafClient() (pb.LeafClient, *grpc.ClientConn) {
	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	return pb.NewLeafClient(conn), conn
}

func TestWorkerConcurrentCalls(functionID *common.FunctionID) error {
	client, conn := createWorkerClient()
	defer conn.Close()

	instanceIDs := make([]string, totalInstances)

	// Start all instances
	for i := 0; i < totalInstances; i++ {
		instanceID, err := client.Start(context.Background(), &pbc.FunctionID{Id: functionID.Id})
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
				response, err := client.Call(context.Background(), &pbc.CallRequest{InstanceId: &pbc.InstanceID{Id: instanceID}, Data: make([]byte, 0), FunctionId: &pbc.FunctionID{Id: functionID.Id}})
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

func createFunction(imageTag string, client *pb.LeafClient) (*common.FunctionID, error) {
	createReq := &pb.CreateFunctionRequest{
		ImageTag: &common.ImageTag{Tag: imageTag},
		Config: &common.Config{
			Memory: RequestedMemory,
			Cpu: &common.CPUConfig{
				Period: RequestedCPUPeriod,
				Quota:  RequestedCPUQuota,
			},
		},
	}

	createFunctionResp, err := (*client).CreateFunction(context.Background(), createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create function: %v", err)
	}

	return createFunctionResp.FunctionID, nil
}

func saveFunctionId(functionID *common.FunctionID, imageTag string) error {
	db, err := sql.Open("sqlite3", SQLITE_DB_PATH)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("INSERT INTO function_images (function_id, image_tag) VALUES (?, ?)", functionID.Id, imageTag)
	if err != nil {
		return fmt.Errorf("failed to insert function id: %v", err)
	}
	return nil
}
