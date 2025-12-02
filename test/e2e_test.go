//go:build e2e

package test

import (
	"bytes"
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

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")

// var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50010")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var HAPROXY_ADDRESS = envOrDefault("HAPROXY_ADDRESS", "localhost:9999")
var ETCD_ADDRESS = envOrDefault("ETCD_ADDRESS", "localhost:2379")

// The timeout used for the calls
const TIMEOUT = 30 * time.Second
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

	// Create HAProxy client for gRPC calls
	haproxyClient := GetHAProxyClient(HAPROXY_ADDRESS)

	//leafClient, conn := GetLeafClient(LEAF_ADDRESS)
	//defer conn.Close()

	data := []struct {
		ImageTag         string
		ExpectedResponse []byte
		Data             []byte
	}{
		{
			ImageTag:         "hyperfaas-hello:latest",
			ExpectedResponse: []byte("HELLO WORLD!"),
			Data:             []byte(""),
		},
		{
			ImageTag:         "hyperfaas-echo:latest",
			ExpectedResponse: []byte("Echo this message"),
			Data:             []byte("Echo this message"),
		},
	}

	for _, d := range data {
		t.Run("Single Call", func(t *testing.T) {
			//t.Skip("Skipping single call test")
			functionId := createFunctionMetadata(d.ImageTag)
			testCall(t, haproxyClient, functionId, d.Data, d.ExpectedResponse, false)
		})
	}

	for _, d := range data {
		t.Run("Concurrent Calls", func(t *testing.T) {
			//t.Skip("Skipping concurrent calls test")
			functionId := createFunctionMetadata(d.ImageTag)
			t.Log("Sending Concurrent Calls")
			wg := sync.WaitGroup{}
			success := atomic.Int32{}
			fail := atomic.Int32{}
			errors := []error{}
			errorsMutex := sync.Mutex{}
			for range CONCURRENCY {
				wg.Go(func() {
					err := testCall(t, haproxyClient, functionId, d.Data, d.ExpectedResponse, false)
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

	haproxyClient := GetHAProxyClient(HAPROXY_ADDRESS)

	functionId := createFunctionMetadataWithOptions("hyperfaas-hello:latest",
		2,
		100000,
	)
	if functionId == "" {
		t.Errorf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionId)

	testCall(t, haproxyClient, functionId, []byte(""), []byte("HELLO WORLD!"), false)

	time.Sleep(2 * time.Second)

	// function should be scaled to zero
	// next call should still work
	testCall(t, haproxyClient, functionId, []byte(""), []byte("HELLO WORLD!"), false)

}

// Tests creating, calling and deleting a function and verifying that further calls to it fail.
func TestDeleteFunction(t *testing.T) {
	t.Skip("Skipping delete function test")

	haproxyClient := GetHAProxyClient(HAPROXY_ADDRESS)

	functionId := createFunctionMetadata("hyperfaas-hello:latest")
	if functionId == "" {
		t.Errorf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionId)

	testCall(t, haproxyClient, functionId, []byte(""), []byte("HELLO WORLD!"), false)

	deleteFunctionMetadata(t, functionId)

	time.Sleep(300 * time.Millisecond)

	// a new call should fail because the function does not exist

	err := testCall(t, haproxyClient, functionId, []byte(""), []byte(""), true)
	if err == nil {
		t.Errorf("Expected call to fail, got %v", err)
	}
}

func envOrDefault(env string, defaultValue string) string {
	if value, ok := os.LookupEnv(env); ok {
		return value
	}
	return defaultValue
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

// tests a call to a function using data. Verifies the response is as expected.
// Receives a leaf or haproxy client.
func testCall(t *testing.T,
	c client,
	functionId string,
	data []byte,
	expectedResponse []byte,
	shouldFail bool,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	resp, err := c.ScheduleCall(ctx, &common.CallRequest{
		FunctionId: functionId,
		Data:       data,
	})
	cancel()
	if err != nil {
		if shouldFail {
			return err
		}
		t.Fail()
		return err
	}
	if resp == nil {
		t.Errorf("Response is nil")
		return nil
	}
	if !bytes.Equal(resp.Data, expectedResponse) {
		t.Errorf("Expected response %s, got %s", expectedResponse, resp.Data)
		return nil
	}
	return nil
}
