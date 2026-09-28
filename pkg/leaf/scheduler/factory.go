package scheduler

import (
	"fmt"

	"hyperfaas-ideal-arch/pkg/core"
)

// NewFromConfig compiles a placement policy document into one scheduler.
//
// Placement is global for the deployment: the leaf builds exactly one scheduler
// and every function actuator shares it. A per-function scheduler would force
// the leaf to retain the union of every function's placement signals, which
// defeats state gating. See docs/DYNAMIC_POLICY_STATE_GATING_DESIGN.md.
func NewFromConfig(cfg *core.PlacementPolicyConfig, workerCount, maxInstancesPerWorker int) (PlacementScheduler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("placement policy is not set")
	}
	switch p := cfg.GetPolicy().(type) {
	case *core.PlacementPolicyConfig_BalancedRoundRobin:
		return NewBalancedRoundRobin(workerCount, maxInstancesPerWorker), nil
	case *core.PlacementPolicyConfig_ResourceAware:
		return NewResourceAware(maxInstancesPerWorker), nil
	case *core.PlacementPolicyConfig_ColdStartAware:
		return NewColdStartAware(maxInstancesPerWorker), nil
	case *core.PlacementPolicyConfig_ReservationAware:
		return NewReservationAware(maxInstancesPerWorker), nil
	case *core.PlacementPolicyConfig_ImageAware:
		return NewImageAware(maxInstancesPerWorker), nil
	case *core.PlacementPolicyConfig_BoundedLoads:
		return NewBoundedLoads(
			workerCount,
			maxInstancesPerWorker,
			p.BoundedLoads.GetBound(),
			int(p.BoundedLoads.GetMaxChainLen()),
		), nil
	default:
		return nil, fmt.Errorf("placement policy is not set")
	}
}

// PlacementPolicyLabel returns the stable name of a configured placement policy.
// It matches the oneof field name and is used only for logs and tests.
func PlacementPolicyLabel(cfg *core.PlacementPolicyConfig) string {
	if cfg == nil {
		return ""
	}
	switch cfg.GetPolicy().(type) {
	case *core.PlacementPolicyConfig_BalancedRoundRobin:
		return "balanced-round-robin"
	case *core.PlacementPolicyConfig_ResourceAware:
		return "resource-aware"
	case *core.PlacementPolicyConfig_ColdStartAware:
		return "cold-start-aware"
	case *core.PlacementPolicyConfig_ReservationAware:
		return "reservation-aware"
	case *core.PlacementPolicyConfig_ImageAware:
		return "image-aware"
	case *core.PlacementPolicyConfig_BoundedLoads:
		return "bounded-loads"
	default:
		return ""
	}
}
