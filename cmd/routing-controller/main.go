package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"strings"

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
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json, dev)")
	logFile := flag.String("log-file", "", "Optional log file path")
	socketPath := flag.String("socket-path", "/var/run/haproxy-spoe.sock", "Socket path to use for communication with HAPROXY")
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
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewServer(logger, *childTypes)

	// Run the cache updater, this listens to the child streams and updates the cache.
	go s.UpdateCacheLoop(ctx)

	a := agent.New(s.handler(), spoeLogger.NewDefaultLog())
	// Run the SPOE agent, this handles the SPOE protocol and routes the requests to the appropriate child.
	if err := a.Serve(listener); err != nil {
		log.Printf("error agent serve: %+v\n", err)
	}

}

type server struct {
	c         *state.ChildCache[string, string]
	l         *slog.Logger
	children  []string
	childType string
}

func NewServer(logger *slog.Logger, childType string) server {
	return server{
		c:         state.NewCache[string, string](),
		l:         logger,
		childType: childType,
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
		if !ok {
			s.l.Error("no children found for function", "function_id", functionID)
			return
		}
		// For now, pick random child
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

func (s *server) ListenToChildStream(ctx context.Context, address string) {
	client, conn, err := getChildClient(s.childType, address)
	if err != nil {
		s.l.Error("failed to get child client", "error", err)
		return
	}
	defer conn.Close()

	stream, err := client.State(ctx, &common.StateRequest{})
	if err != nil {
		s.l.Error("failed to get child stream", "error", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
			for {
				msg, err := stream.Recv()
				if err != nil {
					s.l.Error("failed to receive state from child", "error", err)
					return
				}
				s.l.Debug("received state from child", "address", address, "function_id", msg.FunctionId, "have", msg.Have)
				if msg.Have {
					// we add the address to the cache as a child
					s.c.Append(msg.FunctionId, address)
				} else {
					// we remove the address from the cache as a child
					s.c.Remove(msg.FunctionId, address)
				}
			}
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
