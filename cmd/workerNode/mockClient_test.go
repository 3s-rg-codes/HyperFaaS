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

func buildMockClient(t *testing.T) error {
	var err error
	connection, err = grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Errorf("Could not start client for testing purposes: %v.", err)
		return err
	}
	t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient = pb.NewControllerClient(connection)

	testStartRequest, err = initializeClient(t)
	if err != nil {
		t.Errorf("Could not initialize client for testing purposes: %v.", err)
		return err
	}
	return nil
}

func initializeClient(t *testing.T) (*pb.StartRequest, error) {
	if *imageTagFlag == "" || !strings.Contains(*imageTagFlag, ":") { //some basic syntax criteria for the ImageTag String
		t.Errorf("Image Tag %v not valid: Testing stopped!", *imageTagFlag)
	}
	testImageTag := pb.ImageTag{Tag: *imageTagFlag}
	testConfig := pb.Config{}
	testPayload := pb.StartRequest{
		ImageTag: &testImageTag,
		Config:   &testConfig,
	}
	return &testPayload, nil
}

func determinePassedData(t *testing.T) (s string) {
	switch *passedData {
	case "":
		{
			t.Logf("No data passed to container")
			return ""
		}
	default:
		{
			t.Logf("Data passed to container: %v", *passedData)
			return *passedData
		}
	}
}
