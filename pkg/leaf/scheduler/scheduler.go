package scheduler

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

type PlacementScheduler interface {
	PickWorker(ctx context.Context, function *core.FunctionSpec, workers []*core.WorkerState, demand *core.ScaleDemand) (*core.PlacementDecision, error)
}
