//go:build e2e

package test

import (
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func GetLeafClient(address string) (client leafpb.LeafClient, conn *grpc.ClientConn) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	return leafpb.NewLeafClient(conn), conn
}

func GetWorkerClient(address string) (client workerpb.WorkerClient, conn *grpc.ClientConn) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	return workerpb.NewWorkerClient(conn), conn
}
