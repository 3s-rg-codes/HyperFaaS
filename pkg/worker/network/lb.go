package network

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

// Holds the references to the routers for each function. Each router holds a client and can load balance between the instances.
type CallRouter struct {
	mu      sync.RWMutex
	routers map[string]*Router
	logger  *slog.Logger
}

func NewCallRouter(logger *slog.Logger) *CallRouter {
	return &CallRouter{routers: make(map[string]*Router), logger: logger}
}

func (f *CallRouter) CallFunction(id string, req *common.CallRequest) (*common.CallResponse, error) {
	f.mu.RLock()
	router := f.routers[id]
	f.mu.RUnlock()
	if router == nil {
		return nil, fmt.Errorf("no router found for function ID: %s", id)
	}
	f.logger.Debug("Router state", "router", router.DebugState())
	resp, err := router.Client.Call(context.Background(), req)
	if err != nil {
		f.logger.Error("Error calling function", "id", id, "error", err)
		return nil, err
	} else {
		return resp, nil
	}
}

func (f *CallRouter) AddInstance(id string, addr string) {
	f.mu.Lock()
	router, exists := f.routers[id]
	if !exists {
		f.logger.Debug("Adding new router", "id", id, "addr", addr)
		router = NewRouter([]string{addr}, id)
		f.logger.Debug("New router", "id", id, "addr", addr, "router", router.DebugState())
		f.routers[id] = router
	} else {
		f.logger.Debug("Adding new addr to existing router", "id", id, "addr", addr)
		router.AddAddr(addr)
	}
	f.mu.Unlock()
}

// Updates the router to remove the instance from the list of available instances.
// Must be called when the instance is no longer available to no longer send requests to it.
func (f *CallRouter) HandleInstanceTimeout(id string, addr string) {
	f.mu.RLock()
	router := f.routers[id]
	f.mu.RUnlock()
	if router == nil {
		return
	}
	f.logger.Debug("Removing addr from router", "id", id, "addr", addr)
	router.RemoveAddr(addr)
}

// Updates the router to add the instance to the list of available instances.
func (f *CallRouter) HandleAddInstance(id string, addr string) {
	f.mu.RLock()
	router := f.routers[id]
	f.mu.RUnlock()
	if router == nil {
		return
	}
	router.AddAddr(addr)
}
