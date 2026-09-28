package autoscaling

import (
	"context"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

// Policy computes per-leaf desired instance count from local signals.
// min_instances and max_instances in FunctionSpec.scale are per-leaf; there is no global coordinator.
type Policy interface {
	DesiredScale(ctx context.Context, function *core.FunctionSpec, signals Signals, now time.Time) (*core.ScaleDecision, error)
}

type SignalSource interface {
	CurrentScaleState(ctx context.Context, functionID uint64) (*core.ScaleState, error)
	WatchScaleState(ctx context.Context, functionID uint64) (<-chan *core.ScaleState, <-chan error)
}

// Actuator converges local instance count toward a ScaleDecision by issuing worker RPCs.
type Actuator interface {
	ApplyScale(ctx context.Context, function *core.FunctionSpec, decision *core.ScaleDecision) error
	StartOne(ctx context.Context, reason string) error
	StopAll(ctx context.Context)
	ActualInstances() uint64
}
