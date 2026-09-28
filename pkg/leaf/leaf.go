package leaf

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
	leafstate "hyperfaas-ideal-arch/pkg/leaf/state"
)

type ControlService interface {
	ApplyFunction(ctx context.Context, function *core.FunctionSpec) error
	// Maybe we will need a DrainFunction later. What happens if a function is deleted as it's being called? Or we could fail fast.
	DeleteFunction(ctx context.Context, functionID uint64) error
	// Called by the leaf autoscaler actuator; not used on the invoke hot path.
	EnsureCapacity(ctx context.Context, demand *core.ScaleDemand) error
}

// StateReporter publishes routing state and serves a diagnostic full snapshot.
type StateReporter interface {
	CurrentState(ctx context.Context) (*core.LeafState, error)
	// WatchRoutingState streams projection-gated routing frames for one
	// subscriber. Only the fields named by projection are computed and sent.
	WatchRoutingState(ctx context.Context, configVersion uint64, projection leafstate.RoutingProjection) (<-chan *leafstate.RoutingFrame, <-chan error)
}

type FunctionSubscriber interface {
	SubscribeFunctions(ctx context.Context) (<-chan *core.FunctionEvent, <-chan error)
}
