//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/goforj/godump"
)

const CONCURRENCY = 1500

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// Tests creating a function by writing metadata to etcd
func TestCreateFunctionRequest(t *testing.T) {
	functionID := createFunctionMetadata("hyperfaas-hello:latest")
	if functionID == "" {
		t.Fatalf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionID)
}

// Tests sending a CallRequest through HAProxy
func TestCallRequest(t *testing.T) {
	data := []struct {
		ImageTag       string
		testSingleCall func(*testing.T, string) error
	}{
		{
			ImageTag: "hyperfaas-grpc-proxy-greeter",
			testSingleCall: func(t *testing.T, functionId string) error {
				return testCallGreeter(t, functionId, "World", "Hello, World!", false)
			},
		},
		{
			ImageTag: "hyperfaas-hello",
			testSingleCall: func(t *testing.T, functionId string) error {
				return testCallHello(t, functionId, "Hello, World!", false)
			},
		},
		{
			ImageTag: "hyperfaas-echo",
			testSingleCall: func(t *testing.T, functionId string) error {
				return testCallEcho(t, functionId, []byte("123"), []byte("123"), false)
			},
		},
	}

	for _, d := range data {
		t.Run("Single Call", func(t *testing.T) {
			functionId := createFunctionMetadata(d.ImageTag)
			err := d.testSingleCall(t, functionId)
			if err != nil {
				t.Errorf("testSingleCall failed: %v", err)
			}
		})
	}

	for _, d := range data {
		t.Run("Concurrent Calls", func(t *testing.T) {
			functionId := createFunctionMetadata(d.ImageTag)
			t.Log("Sending Concurrent Calls")

			wg := sync.WaitGroup{}
			success := atomic.Int32{}
			fail := atomic.Int32{}
			errors := []error{}
			errorsMutex := sync.Mutex{}
			for range CONCURRENCY {
				wg.Go(func() {
					err := d.testSingleCall(t, functionId)
					if err != nil {
						fail.Add(1)
						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
					} else {
						success.Add(1)
					}
				})
			}
			wg.Wait()
			if success.Load() != CONCURRENCY {
				t.Errorf("Expected %d successes, got %d", CONCURRENCY, success.Load())
				if len(errors) > 0 {
					// it can get very cluttered if verbose
					if testing.Verbose() {
						t.Log("Error messages: \n")
						godump.Dump(errors)
					} else {
						t.Logf("First error: %v", errors[0])
					}
				}
			}
		})
	}
}

func TestScaleFromZero(t *testing.T) {

	functionId := createFunctionMetadataWithOptions("hyperfaas-hello:latest",
		2,
		100000,
	)
	if functionId == "" {
		t.Errorf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionId)

	err := testCallHello(t, functionId, "Hello, World!", false)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	// function should be scaled to zero
	// next call should still work
	err = testCallHello(t, functionId, "Hello, World!", false)
	if err != nil {
		t.Fatalf("Second call after scale-to-zero failed: %v", err)
	}

}

// Tests creating, calling and deleting a function and verifying that further calls to it fail.
func TestDeleteFunction(t *testing.T) {
	t.Skip("Skipping delete function test")

	functionId := createFunctionMetadata("hyperfaas-hello:latest")
	if functionId == "" {
		t.Errorf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionId)

	testCallHello(t, functionId, "Hello, World!", false)

	deleteFunctionMetadata(t, functionId)

	time.Sleep(300 * time.Millisecond)

	// a new call should fail because the function does not exist

	err := testCallHello(t, functionId, "Hello, World!", true)
	if err == nil {
		t.Errorf("Expected call to fail, got %v", err)
	}
}

// createFunctionMetadata stores the function definition in etcd so leaves/workers discover it.
func createFunctionMetadata(imageTag string) string {
	config := &common.Config{
		Memory: 100 * 1024 * 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  50000,
		},
		MaxConcurrency: 10000,
		Timeout:        15,
	}

	createFunctionReq := &common.CreateFunctionRequest{
		Image:  &common.Image{Tag: imageTag},
		Config: config,
	}

	client, err := metadata.NewClient([]string{ETCD_ADDRESS}, metadata.Options{}, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create metadata client: %v", err))
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	functionID, err := client.PutFunction(ctx, createFunctionReq)
	if err != nil {
		panic(fmt.Sprintf("Failed to store function metadata: %v", err))
	}

	return functionID
}

// createFunctionMetadataWithOptions stores the function definition in etcd so leaves/workers discover it.
// It allows to set the function timeout and max concurrency.
func createFunctionMetadataWithOptions(imageTag string, funcTimeout int32, maxConcurrency int32) string {
	config := &common.Config{
		Memory: 100 * 1024 * 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  50000,
		},
		MaxConcurrency: maxConcurrency,
		Timeout:        funcTimeout,
	}

	createFunctionReq := &common.CreateFunctionRequest{
		Image:  &common.Image{Tag: imageTag},
		Config: config,
	}

	client, err := metadata.NewClient([]string{ETCD_ADDRESS}, metadata.Options{}, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create metadata client: %v", err))
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	functionID, err := client.PutFunction(ctx, createFunctionReq)
	if err != nil {
		panic(fmt.Sprintf("Failed to store function metadata: %v", err))
	}

	return functionID
}

func deleteFunctionMetadata(t *testing.T, functionId string) {
	client, err := metadata.NewClient([]string{ETCD_ADDRESS}, metadata.Options{}, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create metadata client: %v", err))
	}
	defer client.Close()

	err = client.DeleteFunction(context.Background(), functionId)
	if err != nil {
		t.Errorf("Failed to delete function metadata: %v", err)
	}
}
