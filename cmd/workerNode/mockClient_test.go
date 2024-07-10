package main

import (
	"testing"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Initializes a mock client
// Returns the client and the connection for later closing
func BuildMockClient(t *testing.T) (pb.ControllerClient, *grpc.ClientConn) {
	var err error
	connection, err := grpc.NewClient(*controllerServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Errorf("Could not start client for testing purposes: %v.", err)
		return nil, nil
	}
	t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)

	return testClient, connection
}

/*
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
*/
