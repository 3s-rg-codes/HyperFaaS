//go:build e2e

package test

import (
	"bytes"
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"google.golang.org/grpc"
)

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var LB_ADDRESS = envOrDefault("LB_ADDRESS", "localhost:50052")
var DATABASE_ADDRESS = envOrDefault("DATABASE_ADDRESS", "localhost:8999")

// The timeout used for the calls
const TIMEOUT = 60 * time.Second
const CONCURRENCY = 50

func TestMain(m *testing.M) {

	os.Exit(m.Run())
}

// Tests sending a CreateFunctionRequest to the LB
func TestCreateFunctionRequest(t *testing.T) {
	lbc, lbconn := GetLBClient(LB_ADDRESS)
	defer lbconn.Close()
	resp, err := lbc.CreateFunction(context.Background(), &common.CreateFunctionRequest{
		Image: &common.Image{
			Tag: "hyperfaas-hello:latest",
		},
		Config: &common.Config{
			Memory: 100 * 1024 * 1024,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  50000,
			},
		},
	})
	if err != nil {
		t.Errorf("Failed to create function: %v", err)
	}
	if resp.FunctionId == "" {
		t.Errorf("Function id is an empty string: %v", resp)
	}
}

// Tests sending a CallRequest to the LB
func TestCallRequest(t *testing.T) {

	lbc, lbconn := GetLBClient(LB_ADDRESS)
	defer lbconn.Close()
	leafc, leafconn := GetLeafClient(LEAF_ADDRESS)
	defer leafconn.Close()
	//defer leafconn.Close()
	data := []struct {
		ImageTag         string
		ExpectedResponse []byte
		Data             []byte
		Client           client
	}{
		{
			ImageTag:         "hyperfaas-hello:latest",
			ExpectedResponse: []byte("HELLO WORLD!"),
			Data:             []byte(""),
			Client:           leafc,
		},
		{
			ImageTag:         "hyperfaas-echo:latest",
			ExpectedResponse: []byte("Echo this message"),
			Data:             []byte("Echo this message"),
			Client:           leafc,
		},
		{
			ImageTag:         "hyperfaas-hello:latest",
			ExpectedResponse: []byte("HELLO WORLD!"),
			Data:             []byte(""),
			Client:           lbc,
		},
		{
			ImageTag:         "hyperfaas-echo:latest",
			ExpectedResponse: []byte("Echo this message"),
			Data:             []byte("Echo this message"),
			Client:           lbc,
		},
	}

	for _, d := range data {
		t.Run("Single Call", func(t *testing.T) {
			//t.Skip("Skipping single call test")
			functionId := createFunction(d.ImageTag)
			testCall(t, d.Client, functionId, d.Data, d.ExpectedResponse)
		})
	}

	for _, d := range data {
		t.Run("Concurrent Calls", func(t *testing.T) {
			//t.Skip("Skipping concurrent calls test")
			functionId := createFunction(d.ImageTag)
			t.Log("Sending Concurrent Calls")
			wg := sync.WaitGroup{}
			success := atomic.Int32{}
			fail := atomic.Int32{}
			errors := []error{}
			errorsMutex := sync.Mutex{}
			for range CONCURRENCY {
				wg.Go(func() {
					err := testCall(t, d.Client, functionId, d.Data, d.ExpectedResponse)
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
						t.Logf("Error messages: %v", errors)
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

// creates a function and returns the function ID
func createFunction(imageTag string) string {
	c, conn := GetLBClient(LB_ADDRESS)
	defer conn.Close()

	resp, err := c.CreateFunction(context.Background(), &common.CreateFunctionRequest{
		Image: &common.Image{
			Tag: imageTag,
		},
		Config: &common.Config{
			Memory: 100 * 1024 * 1024,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  50000,
			},
			MaxConcurrency: 500,
			Timeout:        10,
		},
	})
	if err != nil {
		panic(err)
	}
	return resp.FunctionId
}

// client interface for the leaf and lb clients
type client interface {
	ScheduleCall(context.Context, *common.CallRequest, ...grpc.CallOption) (*common.CallResponse, error)
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
