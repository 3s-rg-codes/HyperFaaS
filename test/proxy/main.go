// nolint
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/grpc-proxy-greeter/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultLeafProxyAddress = "localhost:50053"
	defaultEtcdAddress      = "localhost:2379"
	greeterImage            = "hyperfaas-grpc-proxy-greeter:latest"
	requestName             = "ProxyTest"

	createTimeout  = 10 * time.Second
	requestTimeout = 45 * time.Second
)

func main() {
	leafAddress := envOrDefault("LEAF_PROXY_ADDRESS", defaultLeafProxyAddress)
	etcdAddress := envOrDefault("ETCD_ADDRESS", defaultEtcdAddress)

	ctx := context.Background()
	functionID, cleanup, err := createGreeterFunction(ctx, etcdAddress)
	if err != nil {
		log.Fatalf("failed to register greeter function: %v", err)
	}
	defer cleanup()
	log.Printf("Created greeter function: %s", functionID)

	// Create gRPC client once and reuse it for all calls
	conn, err := grpc.NewClient(
		leafAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(functionID),
	)
	if err != nil {
		log.Fatalf("failed to create gRPC client: %v", err)
	}
	defer conn.Close()

	client := pb.NewGreeterClient(conn)

	if err := callGreeterViaProxy(client); err != nil {
		log.Fatalf("proxy call failed: %v", err)
	}

	log.Printf("Proxy call succeeded for %s", functionID)

	// now concurrently
	wg := sync.WaitGroup{}
	success := atomic.Int32{}
	fail := atomic.Int32{}
	for range 1000 {
		wg.Go(func() {
			if err := callGreeterViaProxy(client); err != nil {
				fail.Add(1)
				log.Printf("proxy call failed: %v", err)
			} else {
				success.Add(1)
			}
		})
	}
	wg.Wait()
	log.Printf("Success: %d, Fail: %d", success.Load(), fail.Load())
	if success.Load() != 1000 {
		log.Fatalf("expected 1000 successes, got %d", success.Load())
	}
	if fail.Load() != 0 {
		log.Fatalf("expected 0 failures, got %d", fail.Load())
	}

	// concurrent calls for duration
	wg = sync.WaitGroup{}

	success = atomic.Int32{}
	fail = atomic.Int32{}
	ticker := time.NewTicker(1 * time.Second)
	start := time.Now()
	duration := 10 * time.Second
	totalCalls := 10 * 1000
	totalLatency := atomic.Int64{}
	for range ticker.C {
		if time.Now().After(start.Add(duration)) {
			break
		}
		for range 1000 {
			wg.Go(func() {
				s := time.Now()
				if err := callGreeterViaProxy(client); err != nil {
					fail.Add(1)
					log.Printf("proxy call failed: %v", err)
				} else {
					success.Add(1)
					totalLatency.Add(int64(time.Since(s)))
				}
			})
		}
	}
	wg.Wait()
	log.Printf("Success: %d, Fail: %d", success.Load(), fail.Load())
	log.Printf("Average latency: %d ms", (time.Duration(totalLatency.Load()) / time.Duration(totalCalls)).Milliseconds())
}

func createGreeterFunction(ctx context.Context, etcdAddress string) (string, func(), error) {
	metaClient, err := metadata.NewClient([]string{etcdAddress}, metadata.Options{}, nil)
	if err != nil {
		return "", nil, fmt.Errorf("create metadata client: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	req := &common.CreateFunctionRequest{
		Image: &common.Image{Tag: greeterImage},
		Config: &common.Config{
			Memory: 128 * 1024 * 1024,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  100000,
			},
			MaxConcurrency: 10,
			Timeout:        30,
		},
	}

	functionID, err := metaClient.PutFunction(reqCtx, req)
	if err != nil {
		metaClient.Close()
		return "", nil, fmt.Errorf("put function metadata: %w", err)
	}

	cleanup := func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), createTimeout)
		defer cancel()
		if err := metaClient.DeleteFunction(deleteCtx, functionID); err != nil {
			log.Printf("failed to delete function %s: %v", functionID, err)
		}
		if err := metaClient.Close(); err != nil {
			log.Printf("failed to close metadata client: %v", err)
		}
	}

	return functionID, cleanup, nil
}

func callGreeterViaProxy(client pb.GreeterClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	message, err := invokeGreeter(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to reach greeter via proxy: %w", err)
	}

	expected := fmt.Sprintf("Hello, %s!", requestName)
	if message != expected {
		return fmt.Errorf("unexpected greeter response %q (expected %q)", message, expected)
	}
	return nil
}

func invokeGreeter(ctx context.Context, client pb.GreeterClient) (string, error) {
	resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: requestName})
	if err != nil {
		return "", fmt.Errorf("call SayHello: %w", err)
	}
	return resp.GetMessage(), nil
}

func envOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
