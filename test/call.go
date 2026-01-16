//go:build e2e

package test

import (
	"context"
	"os"
	"testing"
	"time"

	bfsPB "github.com/3s-rg-codes/HyperFaaS/functions/go/bfs/pb"
	crashPB "github.com/3s-rg-codes/HyperFaaS/functions/go/crash/pb"
	echoPB "github.com/3s-rg-codes/HyperFaaS/functions/go/echo/pb"
	greeterPB "github.com/3s-rg-codes/HyperFaaS/functions/go/grpc-proxy-greeter/pb"
	helloPB "github.com/3s-rg-codes/HyperFaaS/functions/go/hello/pb"
	sleepPB "github.com/3s-rg-codes/HyperFaaS/functions/go/sleep/pb"
	thumbnailerPB "github.com/3s-rg-codes/HyperFaaS/functions/go/thumbnailer/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// set the env vars to override the default values
var WORKER_ADDRESS = envOrDefault("WORKER_ADDRESS", "localhost:50051")

// var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50010")
var LEAF_ADDRESS = envOrDefault("LEAF_ADDRESS", "localhost:50050")
var HAPROXY_ADDRESS = envOrDefault("HAPROXY_ADDRESS", "localhost:9999")
var ETCD_ADDRESS = envOrDefault("ETCD_ADDRESS", "localhost:2379")

func envOrDefault(env string, defaultValue string) string {
	if value, ok := os.LookupEnv(env); ok {
		return value
	}
	return defaultValue
}

// The timeout used for the calls
const TIMEOUT = 30 * time.Second

// tests a call to a function using data. Verifies the response is as expected.
// Creates a gRPC client connection internally. Supports any request and response type.
func testCallGeneric[Req any, Resp any](t *testing.T,
	functionId string,
	address string,
	caller func(context.Context, *grpc.ClientConn, Req) (Resp, error),
	req Req,
	expectedResponse Resp,
	shouldFail bool,
) error {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithAuthority(functionId))
	if err != nil {
		if shouldFail {
			return err
		}
		t.Fatalf("Failed to create gRPC client: %v", err)
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), TIMEOUT)
	resp, err := caller(ctx, conn, req)
	cancel()
	if err != nil {
		if shouldFail {
			return err
		}
		t.Errorf("Call failed: %v", err)
		return err
	}

	// Use proto.Equal only for response comparison
	equal := proto.Equal(
		any(resp).(proto.Message),
		any(expectedResponse).(proto.Message),
	)

	if !equal {
		t.Errorf("Expected response %v, got %v", expectedResponse, resp)
		return nil
	}
	return nil
}

func testCallGreeter(t *testing.T,
	functionId string,
	message string,
	expectedResponse string,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *greeterPB.HelloRequest) (*greeterPB.HelloReply, error) {
		greeterClient := greeterPB.NewGreeterClient(conn)
		resp, err := greeterClient.SayHello(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &greeterPB.HelloRequest{Name: message}, &greeterPB.HelloReply{Message: expectedResponse}, shouldFail)
}

func testCallHello(t *testing.T,
	functionId string,
	expectedResponse string,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *helloPB.HelloRequest) (*helloPB.HelloReply, error) {
		helloClient := helloPB.NewHelloClient(conn)
		resp, err := helloClient.SayHello(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &helloPB.HelloRequest{}, &helloPB.HelloReply{Message: expectedResponse}, shouldFail)
}

func testCallEcho(t *testing.T,
	functionId string,
	data []byte,
	expectedResponse []byte,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *echoPB.EchoRequest) (*echoPB.EchoReply, error) {
		echoClient := echoPB.NewEchoClient(conn)
		resp, err := echoClient.Echo(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &echoPB.EchoRequest{Data: data}, &echoPB.EchoReply{Data: expectedResponse}, shouldFail)
}

func testCallBFS(t *testing.T,
	functionId string,
	size int32,
	seed *int32,
	expectedResult []int64,
	shouldFail bool,
) error {
	req := &bfsPB.BFSRequest{Size: size}
	if seed != nil {
		req.Seed = seed
	}
	expectedReply := &bfsPB.BFSReply{Result: expectedResult}
	// Note: We don't compare Measurement as it's timing-dependent
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *bfsPB.BFSRequest) (*bfsPB.BFSReply, error) {
		bfsClient := bfsPB.NewBFSClient(conn)
		resp, err := bfsClient.ComputeBFS(ctx, req)
		if err != nil {
			return nil, err
		}
		// Create a response with only Result for comparison (ignore Measurement)
		return &bfsPB.BFSReply{Result: resp.Result}, nil
	}, req, expectedReply, shouldFail)
}

func testCallSleep(t *testing.T,
	functionId string,
	duration time.Duration,
	expectedMessage string,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *sleepPB.SleepRequest) (*sleepPB.SleepReply, error) {
		sleepClient := sleepPB.NewSleepClient(conn)
		resp, err := sleepClient.Sleep(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &sleepPB.SleepRequest{DurationNanos: duration.Nanoseconds()}, &sleepPB.SleepReply{Message: expectedMessage}, shouldFail)
}

func testCallThumbnailer(t *testing.T,
	functionId string,
	image []byte,
	width int32,
	height int32,
	expectedImage []byte,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *thumbnailerPB.ThumbnailRequest) (*thumbnailerPB.ThumbnailReply, error) {
		thumbnailerClient := thumbnailerPB.NewThumbnailerClient(conn)
		resp, err := thumbnailerClient.Thumbnail(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &thumbnailerPB.ThumbnailRequest{Image: image, Width: width, Height: height}, &thumbnailerPB.ThumbnailReply{Image: expectedImage}, shouldFail)
}

func testCallCrash(t *testing.T,
	functionId string,
	shouldFail bool,
) error {
	return testCallGeneric(t, functionId, HAPROXY_ADDRESS, func(ctx context.Context, conn *grpc.ClientConn, req *crashPB.CrashRequest) (*crashPB.CrashReply, error) {
		crashClient := crashPB.NewCrashClient(conn)
		resp, err := crashClient.Crash(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}, &crashPB.CrashRequest{}, &crashPB.CrashReply{}, shouldFail)
}
