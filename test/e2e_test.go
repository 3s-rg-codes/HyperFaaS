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
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/goforj/godump"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var HAPROXY_ADDRESS = envOrDefault("HAPROXY_ADDRESS", "localhost:50052")
var ETCD_ADDRESS = envOrDefault("ETCD_ADDRESS", "localhost:2379")

// The timeout used for the calls
const TIMEOUT = 60 * time.Second
const CONCURRENCY = 50

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// Tests creating a function by writing metadata to etcd
func TestCreateFunctionRequest(t *testing.T) {
	functionID := createFunctionMetadata("hyperfaas-hello:latest")
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
			functionId := createFunctionMetadata(d.ImageTag)
			testCall(t, haproxyClient, functionId, d.Data, d.ExpectedResponse)
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

// createFunctionMetadata stores the function definition in etcd so leaves/workers discover it.

func createFunctionMetadata(imageTag string) string {
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
	deadline := time.Now().Add(TIMEOUT)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
		resp, err := c.ScheduleCall(ctx, &common.CallRequest{
			FunctionId: functionId,
			Data:       data,
		})
		cancel()
		if err != nil {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.NotFound && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			t.Fail()
			return err
		}
		if !bytes.Equal(resp.Data, expectedResponse) {
			t.Errorf("Expected response %s, got %s", expectedResponse, resp.Data)
			return nil
		}
		return nil
	}
}
