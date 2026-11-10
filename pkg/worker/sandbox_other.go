//go:build !linux

package worker

import (
	"fmt"
	"log/slog"
	goruntime "runtime"

	"hyperfaas-ideal-arch/pkg/worker/runtime"
)

func newFirecrackerRuntime(WorkerConfig, *slog.Logger) (runtime.Runtime, error) {
	return nil, fmt.Errorf("runtime type %q is only supported on linux (running on %s)", "firecracker", goruntime.GOOS)
}

func newRuncRuntime(WorkerConfig, *slog.Logger) (runtime.Runtime, error) {
	return nil, fmt.Errorf("runtime type %q is only supported on linux (running on %s)", "runc", goruntime.GOOS)
}
