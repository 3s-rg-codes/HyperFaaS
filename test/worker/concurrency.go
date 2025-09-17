package main

/*
import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	pbc "github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"golang.org/x/sync/errgroup"
)

func TestConcurrentCalls(client pb.ControllerClient, runtime containerRuntime.ContainerRuntime, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {
	totalInstances := 50
	totalFunctions := 10
	totalCallsPerInstance := 100

	instanceIDs := make([]string, totalInstances)
	functions := make([]string, totalFunctions)

	// Start all instances
	for i := 0; i < totalInstances; i++ {
		instanceID, err := client.Start(context.Background(), &pbc.FunctionID{Id: "hyperfaas-hello:latest"})
		if err != nil {
			logger.Error("Error starting instance", "error", err)
			return fmt.Errorf("error starting instance: %v", err)
		}
		instanceIDs[i] = instanceID.InstanceId.Id
	}

	// Send all calls
	ctx := context.Background()

	g, _ := errgroup.WithContext(ctx)
	var successCount, failureCount int32
	var countMu sync.Mutex

	for _, instanceID := range instanceIDs {
		for i := 0; i < totalCallsPerInstance; i++ {
			g.Go(func() error {
				response, err := client.Call(context.Background(), &pbc.CallRequest{InstanceId: &pbc.InstanceID{Id: instanceID}, Data: make([]byte, 0), FunctionId: &pbc.FunctionID{Id: functions[rand.Intn(totalFunctions)]}})
				if err != nil {
					countMu.Lock()
					failureCount++
					countMu.Unlock()
					logger.Error("Error calling instance", "error", err)
					return fmt.Errorf("error calling instance: %v", err)
				}
				if response == nil {
					countMu.Lock()
					failureCount++
					countMu.Unlock()
					logger.Error("No response from instance", "instanceID", instanceID)
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
*/
