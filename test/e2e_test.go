//go:build e2e

package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/goforj/godump"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var HAPROXY_ADDRESS = envOrDefault("HAPROXY_ADDRESS", "localhost:50052")
var DATABASE_ADDRESS = envOrDefault("DATABASE_ADDRESS", "localhost:8999")

// The timeout used for the calls
const TIMEOUT = 60 * time.Second
const CONCURRENCY = 50

func TestMain(m *testing.M) {

	os.Exit(m.Run())
}

// Tests creating a function via the database HTTP API
func TestCreateFunctionRequest(t *testing.T) {
	functionID := createFunctionViaDatabase("hyperfaas-hello:latest")
	if functionID == "" {
		t.Errorf("Function id is an empty string")
	}
	t.Logf("Created function with ID: %s", functionID)
}

// Tests sending a CallRequest through HAProxy
func TestCallRequest(t *testing.T) {

	// Create HAProxy client for gRPC calls
	haproxyClient := GetHAProxyClient(HAPROXY_ADDRESS)

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
			functionId := createFunctionViaDatabase(d.ImageTag)
			testCall(t, haproxyClient, functionId, d.Data, d.ExpectedResponse)
		})
	}

	for _, d := range data {
		t.Run("Concurrent Calls", func(t *testing.T) {
			//t.Skip("Skipping concurrent calls test")
			functionId := createFunctionViaDatabase(d.ImageTag)
			t.Log("Sending Concurrent Calls")
			wg := sync.WaitGroup{}
			success := atomic.Int32{}
			fail := atomic.Int32{}
			errors := []error{}
			errorsMutex := sync.Mutex{}
			for range CONCURRENCY {
				wg.Go(func() {
					err := testCall(t, haproxyClient, functionId, d.Data, d.ExpectedResponse)
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

func envOrDefault(env string, defaultValue string) string {
	if value, ok := os.LookupEnv(env); ok {
		return value
	}
	return defaultValue
}

// creates a function via the database HTTP API and registers it with both leaves... we have to do this until we have etcd working
func createFunctionViaDatabase(imageTag string) string {
	// Create the function via HTTP POST to the database
	functionData := map[string]interface{}{
		"image_tag": imageTag,
		"config": map[string]interface{}{
			"mem_limit":       100 * 1024 * 1024,
			"cpu_quota":       50000,
			"cpu_period":      100000,
			"timeout":         13,
			"max_concurrency": 500,
		},
	}

	jsonData, err := json.Marshal(functionData)
	if err != nil {
		panic(fmt.Sprintf("Failed to marshal function data: %v", err))
	}

	resp, err := http.Post(fmt.Sprintf("http://%s/", DATABASE_ADDRESS), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		panic(fmt.Sprintf("Failed to create function: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		panic(fmt.Sprintf("Failed to create function, status: %d", resp.StatusCode))
	}

	var result struct {
		FunctionID string `json:"function_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		panic(fmt.Sprintf("Failed to decode response: %v", err))
	}

	// Register the function with both leaves
	registerFunctionWithLeaves(result.FunctionID, imageTag)

	return result.FunctionID
}

// registerFunctionWithLeaves registers a function with both leaf services
// TODO: make this configurable and adaptable
func registerFunctionWithLeaves(functionID, imageTag string) {
	// Create the function config for the leaves
	config := &common.Config{
		Memory: 100 * 1024 * 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  50000,
		},
		MaxConcurrency: 500,
		Timeout:        13,
	}

	createFunctionReq := &common.CreateFunctionRequest{
		Image: &common.Image{
			Tag: imageTag,
		},
		Config: config,
	}

	registerReq := &leafpb.RegisterFunctionRequest{
		FunctionId: functionID,
		Config:     createFunctionReq,
	}

	// Register with leaf-A (localhost:50050)
	leafAClient, leafAConn := GetLeafClient("localhost:50050")
	defer leafAConn.Close()

	_, err := leafAClient.RegisterFunction(context.Background(), registerReq)
	if err != nil {
		panic(fmt.Sprintf("Failed to register function with leaf-A: %v", err))
	}

	// Register with leaf-B (localhost:50040)
	leafBClient, leafBConn := GetLeafClient("localhost:50040")
	defer leafBConn.Close()

	_, err = leafBClient.RegisterFunction(context.Background(), registerReq)
	if err != nil {
		panic(fmt.Sprintf("Failed to register function with leaf-B: %v", err))
	}
}

// client interface for gRPC clients
type client interface {
	ScheduleCall(context.Context, *common.CallRequest, ...grpc.CallOption) (*common.CallResponse, error)
}

// GetHAProxyClient creates a gRPC client that connects to HAProxy
func GetHAProxyClient(address string) client {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(fmt.Sprintf("Failed to connect to HAProxy: %v", err))
	}
	// We need to create a client that implements the ScheduleCall method
	// Since HAProxy routes to leaves, we can use the leaf client interface
	return &haproxyClient{conn: conn}
}

type haproxyClient struct {
	conn *grpc.ClientConn
}

func (h *haproxyClient) ScheduleCall(ctx context.Context, req *common.CallRequest, opts ...grpc.CallOption) (*common.CallResponse, error) {
	// Create a leaf client to make the call through HAProxy
	leafClient := leafpb.NewLeafClient(h.conn)
	return leafClient.ScheduleCall(ctx, req, opts...)
}

func (h *haproxyClient) Close() error {
	return h.conn.Close()
}

// tests a call to a function using data. Verifies the response is as expected. Receives a leaf or lb client.
func testCall(t *testing.T, c client, functionId string, data []byte, expectedResponse []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	defer cancel()
	resp, err := c.ScheduleCall(ctx, &common.CallRequest{
		FunctionId: functionId,
		Data:       data,
	})
	if err != nil {
		// not Errorf because the stdout gets cluttered
		t.Fail()
		return err
	}
	if !bytes.Equal(resp.Data, expectedResponse) {
		t.Errorf("Expected response %s, got %s", expectedResponse, resp.Data)
		return nil
	}
	return nil
}
