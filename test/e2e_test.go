//go:build e2e

package test

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var LB_ADDRESS = envOrDefault("LB_ADDRESS", "localhost:50052")
var DATABASE_ADDRESS = envOrDefault("DATABASE_ADDRESS", "localhost:8999")

// The timeout used for the calls
const TIMEOUT = 60 * time.Second

func TestMain(m *testing.M) {

	os.Exit(m.Run())
}

// Tests sending a CallRequest to the LB
func TestCallRequest(t *testing.T) {

	lbc, lbconn := GetLBClient(LB_ADDRESS)
	defer lbconn.Close()
	leafc, leafconn := GetLeafClient(LEAF_ADDRESS)
	defer leafconn.Close()
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
			functionId := createFunction(d.ImageTag)
			testCall(t, d.Client, functionId, d.Data, d.ExpectedResponse)
		})
	}

	for _, d := range data {
		t.Run("Concurrent Calls", func(t *testing.T) {
			functionId := createFunction(d.ImageTag)
			t.Log("Sending 100 Concurrent Calls")
			wg := sync.WaitGroup{}
			for range 100 {
				wg.Go(func() {
					testCall(t, d.Client, functionId, d.Data, d.ExpectedResponse)
				})
			}
			wg.Wait()

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
	c, conn := GetLeafClient(LEAF_ADDRESS)
	defer conn.Close()

	resp, err := c.CreateFunction(context.Background(), &leafpb.CreateFunctionRequest{
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
func testCall(t *testing.T, c client, functionId string, data []byte, expectedResponse []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	defer cancel()
	resp, err := c.ScheduleCall(ctx, &common.CallRequest{
		FunctionId: functionId,
		Data:       data,
	})
	if err != nil {
		t.Errorf("Failed to send call: %v", err)
		return
	}
	if !bytes.Equal(resp.Data, expectedResponse) {
		t.Errorf("Expected response %s, got %s", expectedResponse, resp.Data)
	}
}
