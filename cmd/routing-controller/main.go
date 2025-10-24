package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/state"
	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	rcpb "github.com/3s-rg-codes/HyperFaaS/proto/routingcontroller"
	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/agent"
	spoeLogger "github.com/negasus/haproxy-spoe-go/logger"
	"github.com/negasus/haproxy-spoe-go/request"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var childAddrs utils.StringList

	address := flag.String("address", "0.0.0.0:50051", "Routing controller listen address")
	grpcAddress := flag.String("grpc-address", "0.0.0.0:50052", "Routing controller gRPC listen address")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json, dev)")
	logFile := flag.String("log-file", "", "Optional log file path")
	socketPath := flag.String("socket-path", "/var/run/haproxy-spoe.sock", "Socket path to use for communication with HAPROXY")
	root := flag.Bool("root", true, "Whether this is the root routing controller")
	//spoeLoggerPath := flag.String("spoe-logger-path", "", "Path to file to write SPOE agent log to")

	flag.Var(&childAddrs, "child-addr", "Child address (repeat for multiple children)")
	childTypes := flag.String("child-types", "leaf", "Type of children (leaf or routing-controller)")

	flag.Parse()

	logger := utils.SetupLogger(*logLevel, *logFormat, *logFile)
	logger.Info("Starting Routing Controller",
		"address", *address,
		"children", childAddrs,
	)

	_ = os.Remove(*socketPath)
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Printf("error create listener, %v", err)
		os.Exit(1)
	}

	// Set socket permissions to allow HAProxy to connect
	// I couldnt solve this directly in the docker compose file, so I'm doing it here
	if err := os.Chmod(*socketPath, 0666); err != nil {
		log.Printf("warning: failed to set socket permissions: %v", err)
	}

	logger.Info("SPOE socket created", "path", *socketPath)
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewServer(logger, *childTypes, childAddrs, *root)

	// Run the cache updater, this listens to the child streams and updates the cache.
	go s.UpdateCacheLoop(ctx)

	// Run the gRPC server, this allows parent routing controllers to subscribe to state changes.
	go func() {
		// only non-root routing controllers run the gRPC server.
		if *root {
			return
		}
		listener, err := net.Listen("tcp", *grpcAddress)
		if err != nil {
			logger.Error("failed to listen", "error", err)
			os.Exit(1)
		}

		grpcServer := grpc.NewServer(
			grpc.ChainUnaryInterceptor(utils.InterceptorLogger(logger)),
		)
		rcpb.RegisterRoutingControllerServer(grpcServer, &s)
		logger.Info("routing controller gRPC server ready", "address", listener.Addr())
		if err := grpcServer.Serve(listener); err != nil {
			logger.Error("gRPC server stopped", "error", err)
		}
	}()

	a := agent.New(s.handler(), spoeLogger.NewDefaultLog())
	// Run the SPOE agent, this handles the SPOE protocol and routes the requests to the appropriate child.
	if err := a.Serve(listener); err != nil {
		log.Printf("error agent serve: %+v\n", err)
	}

}

type server struct {
	rcpb.UnimplementedRoutingControllerServer
	c         *state.ChildCache[string, string]
	l         *slog.Logger
	children  []string
	childType string
}

func NewServer(logger *slog.Logger, childType string, children []string, root bool) server {
	return server{
		// only non-root routing controllers send updates about the state of the children to the parent.
		c:         state.NewCache[string, string](!root),
		l:         logger,
		childType: childType,
		children:  children,
	}
}
func (s *server) handler() func(req *request.Request) {
	return func(req *request.Request) {
		s.l.Debug("handle request",
			"EngineID", req.EngineID,
			"StreamID", req.StreamID,
			"FrameID", req.FrameID,
			"message_count", req.Messages.Len(),
		)

		mes, err := req.Messages.GetByName("route-decision")
		if err != nil {
			s.l.Error("message route-decision not found", "error", err)
			return
		}

		// Prefer :authority (gRPC/HTTP2) over Host
		var functionID string
		if authVal, ok := mes.KV.Get("authority"); ok {
			if auth, ok2 := authVal.(string); ok2 && auth != "" {
				functionID = auth
			}
		}
		if functionID == "" {
			if hostVal, ok := mes.KV.Get("host"); ok {
				if h, ok2 := hostVal.(string); ok2 && h != "" {
					functionID = h
				}
			}
		}
		if functionID == "" {
			s.l.Error("variables 'authority' and 'host' not found or empty in message")
			return
		}

		// Use explicit protocol from message if provided
		protocol := "http"
		if protoVal, ok := mes.KV.Get("protocol"); ok {
			if p, ok2 := protoVal.(string); ok2 && p != "" {
				protocol = strings.ToLower(p)
			}
		} else if _, ok := mes.KV.Get("authority"); ok {
			// Fallback for gRPC
			protocol = "grpc"
		} else if strings.Contains(strings.ToLower(req.EngineID), "grpc") {
			protocol = "grpc"
		}

		children, ok := s.c.Get(functionID)
		if !ok || len(children) == 0 {
			if len(s.children) == 0 {
				s.l.Error("no children configured", "function_id", functionID)
				return
			}
			child := s.children[rand.Intn(len(s.children))]
			s.l.Debug("cache miss, assigning default child", "function_id", functionID, "child", child)
			s.c.Append(functionID, child)
			children = []string{child}
		}

		child := children[rand.Intn(len(children))]

		s.l.Debug("routing request to child", "function_id", functionID, "child", child, "protocol", protocol)

		// With 'option var-prefix routing' in SPOE config, these will be available as
		// var(txn.routing.preferred_backend) and var(txn.routing.routed_by)
		req.Actions.SetVar(action.ScopeTransaction, "preferred_backend", child)
		req.Actions.SetVar(action.ScopeTransaction, "routed_by", "routing-controller")
	}
}

func (s *server) UpdateCacheLoop(ctx context.Context) {
	for _, address := range s.children {
		go s.ListenToChildStream(ctx, address)
	}
}

// a child can be a Leaf or another routing controller
type child interface {
	State(ctx context.Context, req *common.StateRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[common.StateResponse], error)
}

// ListenToChildStream calls consumeChildStream for each child. If the stream is closed, it will back off and try again.
func (s *server) ListenToChildStream(ctx context.Context, address string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		client, conn, err := getChildClient(s.childType, address)
		if err != nil {
			s.l.Error("failed to get child client", "address", address, "error", err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff)
			continue
		}

		s.l.Info("connected to child", "address", address)
		backoff = time.Second

		err = s.consumeChildStream(ctx, client, address)
		_ = conn.Close()

		if err == nil || errors.Is(err, context.Canceled) {
			return
		}

		if errors.Is(err, io.EOF) {
			s.l.Info("child stream closed", "address", address)
		} else {
			s.l.Warn("child stream error", "address", address, "error", err)
		}

		sleepWithContext(ctx, backoff)
		backoff = nextBackoff(backoff)
	}
}

// consumeChildStream consumes the child stream and updates the cache.
func (s *server) consumeChildStream(ctx context.Context, client child, address string) error {
	stream, err := client.State(ctx, &common.StateRequest{})
	if err != nil {
		return err
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if msg.GetFunctionId() == "" {
			continue
		}

		s.l.Debug("received state from child", "address", address, "function_id", msg.FunctionId, "have", msg.Have)
		if msg.Have {
			s.c.Append(msg.FunctionId, address)
		} else {
			s.c.Remove(msg.FunctionId, address)
		}
	}
}

func getChildClient(childType string, address string) (child, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	if childType == "leaf" {
		return leafpb.NewLeafClient(conn), conn, nil
	}
	if childType == "routing-controller" {
		return rcpb.NewRoutingControllerClient(conn), conn, nil
	}
	return nil, nil, fmt.Errorf("invalid child type: %s", childType)
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		d = time.Millisecond * 100
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func nextBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return time.Second
	}
	next := current * 2
	if next > 10*time.Second {
		return 10 * time.Second
	}
	return next
}

// State streams changes in state of a function_id.
func (s *server) State(req *common.StateRequest, stream rcpb.RoutingController_StateServer) error {
	ctx := stream.Context()
	for update := range s.c.Updates() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := stream.Send(&common.StateResponse{
			FunctionId: update.Id,
			Have:       update.Have,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
