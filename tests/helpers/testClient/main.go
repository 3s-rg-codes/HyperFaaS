package main

import (
	"context"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"
	"os"
	"time"
)

const (
	workerAddress = "worker:50051"
	imageTag      = "hyperfaas-hello:latest"
)

func main() {
	handler := slog.NewTextHandler(os.Stdout, nil)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	logger.Info("Hi from client")

	time.Sleep(3 * time.Second)

	var err error
	connection, err := grpc.NewClient(workerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}

	//t.Logf("Client for testing purposes (%v) started with target %v", connection, *controllerServerAddress)
	testClient := pb.NewControllerClient(connection)

	start, err := testClient.Start(context.Background(), &common.FunctionID{Id: imageTag})
	if err != nil {
		panic(err)
	}

	logger.Info(start.Id)

	for {

	}
}
