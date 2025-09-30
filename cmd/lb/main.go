package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	kv "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"
	_ "github.com/3s-rg-codes/HyperFaaS/pkg/lb" // Register custom load balancer
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/lb"
	"github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

type lbServer struct {
	lb.UnimplementedLBServer
	lbClient   lb.LBClient
	leafClient leaf.LeafClient
	// list of leaf nodes
	leafAddrs []string
	// list of LB nodes
	lbAddrs []string
	logger  *slog.Logger
	// function metadata database client
	database kv.FunctionMetadataStore
}

func (l *lbServer) ScheduleCall(ctx context.Context, req *common.CallRequest) (*common.CallResponse, error) {

	if len(l.leafAddrs) > 0 {
		l.logger.Debug("Forwarding to leaf node", "function_id", req.FunctionId)
		return l.leafClient.ScheduleCall(ctx, req)
	} else if len(l.lbAddrs) > 0 {
		l.logger.Debug("Forwarding to other LB node", "function_id", req.FunctionId)
		return l.lbClient.ScheduleCall(ctx, req)
	} else {
		l.logger.Error("No downstream nodes available", "function_id", req.FunctionId)
		return nil, fmt.Errorf("no downstream nodes available for function %s", req.FunctionId)
	}
}

// CreateFunction stores the function metadata in the database and broadcasts
// the creation to all connected leaf nodes so they can initialize local state.
// Returns the created function ID.
func (l *lbServer) CreateFunction(ctx context.Context, req *common.CreateFunctionRequest) (*common.CreateFunctionResponse, error) {

	if l.database == nil {
		l.logger.Error("Database client not configured for LB")
		return nil, fmt.Errorf("database client not configured")
	}

	functionID, err := l.database.Put(req.Image, req.Config)
	if err != nil {
		l.logger.Error("Failed to store function metadata", "error", err)
		return nil, fmt.Errorf("failed to store function metadata: %w", err)
	}

	if len(l.leafAddrs) > 0 {
		for _, addr := range l.leafAddrs {
			err := l.broadcastCreateToLeaf(ctx, addr, &leaf.RegisterFunctionRequest{Config: req, FunctionId: functionID})
			if err != nil {
				l.logger.Warn("Broadcast to leaf failed", "addr", addr, "error", err)
				// todo: maybe handle partial failure here
				return nil, err
			}
		}
	}

	return &common.CreateFunctionResponse{FunctionId: functionID}, nil
}

func (l *lbServer) broadcastCreateToLeaf(ctx context.Context, addr string, req *leaf.RegisterFunctionRequest) error {
	_, err := utils.CallWithRetry(ctx, func() (struct{}, error) {
		// we need to create a new client here because if we use l.leafClient, it will use the load balancer.
		// TODO: find an actually good pub/sub solution for this.
		conn, dialErr := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if dialErr != nil {
			return struct{}{}, fmt.Errorf("failed to dial leaf %s: %w", addr, dialErr)
		}
		client := leaf.NewLeafClient(conn)
		_, callErr := client.RegisterFunction(ctx, req)
		_ = conn.Close()
		if callErr != nil {
			return struct{}{}, fmt.Errorf("leaf %s RegisterFunction error: %w", addr, callErr)
		}
		return struct{}{}, nil
	}, 3, 300*time.Millisecond)
	return err
}

// Helper function to create gRPC client with load balancer
func createClientConnection(addrs []string, scheme string, strategy string) (*grpc.ClientConn, error) {
	if len(addrs) == 0 {
		return nil, errors.New("no addresses provided")
	}

	mr := manual.NewBuilderWithScheme(scheme)
	var endpoints []resolver.Endpoint
	for _, addr := range addrs {
		endpoints = append(endpoints, resolver.Endpoint{Addresses: []resolver.Address{{Addr: addr}}})
	}

	serviceConfigJSON := fmt.Sprintf(`{"loadBalancingConfig": [{"%s":{}}]}`, strategy)
	mr.InitialState(resolver.State{
		Endpoints: endpoints,
	})

	cc, err := grpc.NewClient(
		mr.Scheme()+":///",
		grpc.WithResolvers(mr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfigJSON),
	)

	return cc, err
}

type addressList []string

func (a *addressList) String() string {
	return strings.Join(*a, ",")
}

func (a *addressList) Set(value string) error {
	*a = append(*a, value)
	return nil
}

func main() {
	var leafAddrs addressList
	var lbAddrs addressList

	address := flag.String("address", "0.0.0.0:50052", "The address to listen on")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (json, text or dev)")
	logFilePath := flag.String("log-file", "", "Log file path (defaults to stdout)")
	strategy := flag.String("strategy", "round_robin", "Load balancing strategy (round_robin, random)")
	databaseAddress := flag.String("database-address", "http://database:8999", "Function metadata database address (HTTP)")

	flag.Var(&leafAddrs, "leaf-addr", "Address of a leaf node to connect to (can be specified multiple times)")
	flag.Var(&lbAddrs, "lb-addr", "Address of another LB node to connect to (can be specified multiple times)")

	flag.Parse()

	// provide leaf nodes or LB nodes, never both
	if len(leafAddrs) > 0 && len(lbAddrs) > 0 {
		fmt.Fprintf(os.Stderr, "cannot specify both leaf addresses and LB addresses\n")
		os.Exit(1)
	}

	if len(leafAddrs) == 0 && len(lbAddrs) == 0 {
		fmt.Fprintf(os.Stderr, "must specify at least one leaf address or LB address\n")
		os.Exit(1)
	}

	logger := utils.SetupLogger(*logLevel, *logFormat, *logFilePath)
	logger.Info("Starting HyperFaaS Load Balancer",
		"address", *address,
		"leaf_addrs", leafAddrs,
		"lb_addrs", lbAddrs,
		"strategy", *strategy)

	server := &lbServer{
		leafAddrs: leafAddrs,
		lbAddrs:   lbAddrs,
		logger:    logger,
	}

	server.database = kv.NewHttpDBClient(*databaseAddress, logger)

	if len(leafAddrs) > 0 {
		cc, err := createClientConnection(leafAddrs, "leaf-lb", *strategy)
		if err != nil {
			logger.Error("Failed to create leaf client connection", "error", err)
			os.Exit(1)
		}
		server.leafClient = leaf.NewLeafClient(cc)
		logger.Info("Connected to leaf nodes", "count", len(leafAddrs))
	} else {
		cc, err := createClientConnection(lbAddrs, "lb-lb", *strategy)
		if err != nil {
			logger.Error("Failed to create LB client connection", "error", err)
			os.Exit(1)
		}
		server.lbClient = lb.NewLBClient(cc)
		logger.Info("Connected to LB nodes", "count", len(lbAddrs))
	}

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(utils.InterceptorLogger(logger)),
	)
	lb.RegisterLBServer(grpcServer, server)

	logger.Info("Load balancer server started", "address", listener.Addr())

	if err := grpcServer.Serve(listener); err != nil {
		logger.Error("Failed to serve", "error", err)
		os.Exit(1)
	}
}
