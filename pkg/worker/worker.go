package worker

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

// SandboxService manages function sandboxes, including image preparation and readiness signaling.
type SandboxService interface {
	PrepareImage(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error)
	HasImage(ctx context.Context, image string) (bool, error)
	CreateSandbox(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error)
	StopSandbox(ctx context.Context, instanceID uint64) error
	ListSandboxes(ctx context.Context) ([]*core.InstanceState, error)
	SignalReady(ctx context.Context, instanceID uint64) error
	// CachedImages returns a snapshot of images successfully prepared on this worker.
	CachedImages() []*core.CachedImage
}

// StateProjection selects which optional worker-state fields a state stream
// subscriber needs. Lifecycle-authoritative state (sandbox states, instance
// count, capacity, allocated resources) is always produced; only the gated
// placement signals are optional. The gate is applied at the source: an
// unrequested field is never computed or allocated, not merely omitted from the
// wire message. See docs/DYNAMIC_POLICY_STATE_GATING_DESIGN.md.
type StateProjection struct {
	// LoadAverageNorm requests WorkerState.load_average_norm.
	LoadAverageNorm bool
	// CachedImages requests WorkerState.cached_images.
	CachedImages bool
}

// FullStateProjection computes every field. It is used by the diagnostic
// CurrentState path, which is ungated by design.
func FullStateProjection() StateProjection {
	return StateProjection{LoadAverageNorm: true, CachedImages: true}
}

type HealthService interface {
	// CurrentState builds a diagnostic full snapshot. It is not projection
	// gated: it always computes every field and is only used by tooling/tests.
	CurrentState(ctx context.Context) (*core.WorkerState, error)
	WatchState(ctx context.Context, projection StateProjection) (<-chan *core.WorkerState, <-chan error)
}
