package network

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	function "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

// Uses grpc load balancing to round robin between running instances. Belongs to a single function.
type Router struct {
	mu          sync.RWMutex
	activeAddrs []string
	cc          *grpc.ClientConn
	mr          *manual.Resolver
	Client      function.FunctionServiceClient
}

func NewRouter(addrs []string, scheme string) *Router {
	mr := manual.NewBuilderWithScheme("custom-lb")
	var endpoints []resolver.Endpoint
	for _, addr := range addrs {
		endpoints = append(endpoints, resolver.Endpoint{Addresses: []resolver.Address{{Addr: addr}}})
	}

	serviceConfigJSON := `{"loadBalancingConfig": [{"round_robin":{}}]}`
	is := resolver.State{
		Endpoints: endpoints,
	}
	log.Printf("Initial state: %v", is)
	mr.InitialState(is)

	cc, err := grpc.NewClient(
		mr.Scheme()+":///",
		grpc.WithResolvers(mr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfigJSON),
	)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	client := function.NewFunctionServiceClient(cc)

	return &Router{activeAddrs: addrs, cc: cc, mr: mr, Client: client}
}

func (r *Router) AddAddr(addr string) {
	r.mu.Lock()
	r.activeAddrs = append(r.activeAddrs, addr)
	newEndpoints := []resolver.Endpoint{}
	for _, addr := range r.activeAddrs {
		newEndpoints = append(newEndpoints, resolver.Endpoint{Addresses: []resolver.Address{{Addr: addr}}})
	}
	r.mr.UpdateState(resolver.State{Endpoints: newEndpoints})
	r.mu.Unlock()
}

func (r *Router) RemoveAddr(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var newActiveAddrs []string
	for _, a := range r.activeAddrs {
		if a != addr {
			newActiveAddrs = append(newActiveAddrs, a)
		}
	}
	r.activeAddrs = newActiveAddrs

	newEndpoints := []resolver.Endpoint{}
	for _, a := range newActiveAddrs {
		newEndpoints = append(newEndpoints, resolver.Endpoint{Addresses: []resolver.Address{{Addr: a}}})
	}
	r.mr.UpdateState(resolver.State{Endpoints: newEndpoints})
}

func (r *Router) DebugState() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fmt.Sprintf("Active addrs: %v, cc: %v, mr: %v", r.activeAddrs, r.cc, r.mr)
}

func DebugCallAddr(addr string) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	client := function.NewFunctionServiceClient(conn)
	log.Printf(" DEBUG: Calling function at %s", addr)
	resp, err := client.Call(context.Background(), &common.CallRequest{FunctionId: &common.FunctionID{Id: "test"}})
	if err != nil {
		log.Fatalf("failed to debugcall function: %v", err)
	}
	log.Printf(" DEBUG: Call response: %v", resp)
}
