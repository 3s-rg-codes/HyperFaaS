package main

import (
	"context"
	"flag"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"strings"
	"testing"
)

var (
	testClient       pb.ControllerClient
	testCtx          = context.Background()
	testStartRequest *pb.StartRequest
)

func buildMockClient(t *testing.T) {
	connection, err := grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Could not start client for testing purposes: %v. Testing stopped!", err)
	}
	t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient = pb.NewControllerClient(connection)

	testStartRequest = initializeClient(t)

	defer func(connection *grpc.ClientConn) {
		err := connection.Close()
		if err != nil {
			t.Fatalf("Connection (%s) couldnt be closed orderly (err: %s) Testing stopped!", connection, err)
		}
	}(connection)
}

func initializeClient(t *testing.T) *pb.StartRequest {
	flag.Parse()
	//
	if *imageTagFlag == "" || !strings.Contains(*imageTagFlag, ":") {
		t.Fatalf("Image Tag %v not valid: Testing stopped!", *imageTagFlag)
	}
	testImageTag := pb.ImageTag{Tag: *imageTagFlag}
	testConfig := pb.Config{}
	testPayload := pb.StartRequest{
		ImageTag: &testImageTag,
		Config:   &testConfig,
	}
	return &testPayload
}
