package main

import (
	"context"
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
	connection       *grpc.ClientConn
)

func buildMockClient(t *testing.T) {
	var err error
	connection, err = grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Errorf("Could not start client for testing purposes: %v. Testing stopped!", err)
	}
	t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient = pb.NewControllerClient(connection)

	testStartRequest = initializeClient(t)
}

func initializeClient(t *testing.T) *pb.StartRequest {
	if *imageTagFlag == "" || !strings.Contains(*imageTagFlag, ":") {
		t.Errorf("Image Tag %v not valid: Testing stopped!", *imageTagFlag)
	}
	testImageTag := pb.ImageTag{Tag: *imageTagFlag}
	testConfig := pb.Config{}
	testPayload := pb.StartRequest{
		ImageTag: &testImageTag,
		Config:   &testConfig,
	}
	return &testPayload
}
