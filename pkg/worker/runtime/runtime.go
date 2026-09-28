package runtime

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

// LifecycleEvent reports an asynchronous sandbox lifecycle change.
type LifecycleEvent int

const (
	LifecycleExit LifecycleEvent = iota
	LifecycleCrash
	LifecycleOOM
)

// Runtime prepares and manages function sandboxes.
//
// Each function declares a protocol in RuntimeSpec.protocol ("http" or "grpc").
// Function instances listen on port 50052; HTTP and gRPC may be multiplexed on that port.
type Runtime interface {
	Prepare(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error)
	HasImage(ctx context.Context, image string) (bool, error)
	Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error)
	Stop(ctx context.Context, instanceID uint64) error
	Stats(ctx context.Context, instanceID uint64) (*core.ResourceUsage, error)
}

// LifecycleWatcher is implemented by runtimes that can monitor sandbox exit/crash events.
type LifecycleWatcher interface {
	WatchLifecycle(ctx context.Context, instanceID uint64) (<-chan LifecycleEvent, error)
}
