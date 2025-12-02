//go:build e2e

package test

import (
	"context"
	"fmt"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// client interface for gRPC clients
type client interface {
	ScheduleCall(context.Context, *common.CallRequest, ...grpc.CallOption) (*common.CallResponse, error)
}

// GetHAProxyClient creates a gRPC client that connects to HAProxy
func GetHAProxyClient(address string) client {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(fmt.Sprintf("Failed to connect to HAProxy: %v", err))
	}
	// We need to create a client that implements the ScheduleCall method
	// Since HAProxy routes to leaves, we can use the leaf client interface
	return &haproxyClient{conn: conn}
}

type haproxyClient struct {
	conn *grpc.ClientConn
}

func (h *haproxyClient) ScheduleCall(ctx context.Context, req *common.CallRequest, opts ...grpc.CallOption) (*common.CallResponse, error) {
	// Create a leaf client to make the call through HAProxy
	leafClient := leafpb.NewLeafClient(h.conn)
	return leafClient.ScheduleCall(ctx, req, opts...)
}

func (h *haproxyClient) Close() error {
	return h.conn.Close()
}

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
