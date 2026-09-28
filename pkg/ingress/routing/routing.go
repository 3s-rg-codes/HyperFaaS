// Package routing implements ingress leaf routing as pluggable, state-gated
// policies.
//
// A policy is compiled into a model that retains only the state it reads. The
// model owns its indexes and cached values and publishes a Picker for requests.
// Pickers can read immutable copies or policy-owned atomic values. There is no
// shared routing snapshot.
package routing

import (
	"context"
	"log/slog"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RouteRequest is the compact per-request routing input. It holds only routing
// identity: never the body, headers, or any protobuf request.
type RouteRequest struct {
	UserID     uint64
	FunctionID uint64
}

// LeafAddress is the configured network location of one leaf.
type LeafAddress struct {
	// HTTPAddress is the leaf's internal streaming HTTP invocation address.
	HTTPAddress string
	// ControlAddress is the leaf's gRPC LeafControlService address, used for
	// routing-state streams.
	ControlAddress string
}

// LeafTarget is the compact routing result handed to the reverse proxy.
type LeafTarget struct {
	LeafID      uint64
	HTTPAddress string
	Reason      Reason
}

// Picker is a policy-specific request-time view. Its structure is immutable.
// It may read atomic values or immutable copies published by its model. Pick
// must be safe for concurrent calls and must not take the controller mutex.
type Picker interface {
	Pick(RouteRequest) (LeafTarget, error)
}

// ErrRoutingUnavailable is returned while no routing policy is active, which
// happens between clearing the old policy and installing the new one during a
// configuration reload. The request path maps it to 503.
var ErrRoutingUnavailable = status.Error(codes.Unavailable, "routing unavailable")

func noHealthyLeaves() error {
	return status.Error(codes.Unavailable, "no healthy leaves available")
}

// activeRouting is the immutable policy-plus-version published to the request
// path.
type activeRouting struct {
	configVersion uint64
	picker        Picker
}

// Engine is the ingress request-side routing entry point.
//
// It holds one atomic pointer to the active policy. A configuration reload
// clears the pointer, so requests return ErrRoutingUnavailable, and installs
// the new policy once its state baseline is ready. This is acceptable because
// policy configuration changes only while no invocation load is in flight.
type Engine struct {
	active atomic.Pointer[activeRouting]
	logger *slog.Logger
}

func NewEngine() *Engine { return &Engine{} }

// Pick routes one request with the active policy. A chosen leaf is logged at
// debug. A failure is logged at warn. Both checks return before formatting
// when the logger is unset or the level is disabled.
func (e *Engine) Pick(req RouteRequest) (LeafTarget, error) {
	active := e.active.Load()
	if active == nil || active.picker == nil {
		e.warnDecision(req, ErrRoutingUnavailable)
		return LeafTarget{}, ErrRoutingUnavailable
	}
	target, err := active.picker.Pick(req)
	if err != nil {
		e.warnDecision(req, err)
		return LeafTarget{}, err
	}
	if e.logger != nil && e.logger.Enabled(context.Background(), slog.LevelDebug) {
		e.logger.Debug("routing decision",
			"function_id", req.FunctionID,
			"leaf_id", target.LeafID,
			"reason", target.Reason.String(),
		)
	}
	return target, nil
}

func (e *Engine) warnDecision(req RouteRequest, err error) {
	if e.logger == nil {
		return
	}
	e.logger.Warn("routing decision failed", "function_id", req.FunctionID, "error", err)
}

// ConfigVersion returns the active configuration version, or 0 while routing is
// unavailable.
func (e *Engine) ConfigVersion() uint64 {
	if active := e.active.Load(); active != nil {
		return active.configVersion
	}
	return 0
}

func (e *Engine) install(picker Picker, version uint64) {
	e.active.Store(&activeRouting{configVersion: version, picker: picker})
}

func (e *Engine) clear() {
	e.active.Store(nil)
}

// hashFunctionID spreads function IDs across a leaf list. It uses an
// allocation-free splitmix finalizer instead of hashing a decimal string.
func hashFunctionID(functionID uint64) uint64 {
	x := functionID
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
