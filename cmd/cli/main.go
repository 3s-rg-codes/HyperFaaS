package main

import (
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/golang-cz/devslog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
	"log/slog"
	"os"
)

func main() {

	devOpts := &devslog.Options{
		HandlerOptions: &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelInfo,
		},
		MaxSlicePrintSize: 5,
		SortKeys:          true,
		NewLineAfterLog:   true,
		StringerFormatter: true,
	}

	handler := devslog.NewHandler(os.Stdout, devOpts)

	conn, err := grpc.NewClient("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	leafClient := pb.NewLeafClient(conn)

	Cli = CliStruct{
		leafConn: leafClient,
		logger:   slog.New(handler),
	}

	Execute()
}
