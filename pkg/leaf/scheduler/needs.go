package scheduler

import (
	"fmt"

	"hyperfaas-ideal-arch/pkg/core"
)

// PlacementNeeds is the set of gated worker signals a placement policy reads.
//
// It is the placement-domain contract, separate from ingress RoutingNeeds
// because the fields and meanings differ. Lifecycle-authoritative worker state
// (sandbox states, instance count, capacity, allocated resources) is always
// sent and is not part of this set; only the optional placement signals are
// gated. The leaf turns these needs into the projection it opens on each worker
// stream, so a policy only pays for the state it reads.
type PlacementNeeds uint32

const (
	// NeedWorkerLoad is WorkerState.load_average_norm, the normalized 1-minute
	// load average. Used by bounded-loads placement and by the leaf_load signal
	// read by load-aware ingress routing policies.
	NeedWorkerLoad PlacementNeeds = 1 << iota
	// NeedWorkerImages is WorkerState.cached_images. Used by image-aware
	// placement only.
	NeedWorkerImages
)

// Has reports whether every bit in x is set in n.
func (n PlacementNeeds) Has(x PlacementNeeds) bool { return n&x == x }

// NeedsFor returns the gated worker signals a configured placement policy reads.
func NeedsFor(cfg *core.PlacementPolicyConfig) (PlacementNeeds, error) {
	if cfg == nil {
		return 0, fmt.Errorf("placement policy is not set")
	}
	switch cfg.GetPolicy().(type) {
	case *core.PlacementPolicyConfig_BalancedRoundRobin:
		// Pure rotation; no worker telemetry.
		return 0, nil
	case *core.PlacementPolicyConfig_ResourceAware:
		// Spreads on observed host pressure. ResourceAware reads only
		// WorkerState.capacity and .allocated, which are always sent as
		// lifecycle state, plus the leaf-overlaid instance count. It does not
		// read load_average_norm, so it requests no gated signal.
		return 0, nil
	case *core.PlacementPolicyConfig_ColdStartAware:
		// Uses leaf-local pending starts, not worker telemetry.
		return 0, nil
	case *core.PlacementPolicyConfig_ReservationAware:
		// Uses leaf-local instances and reservations, not worker telemetry.
		return 0, nil
	case *core.PlacementPolicyConfig_ImageAware:
		// Prefers workers that advertise the function image.
		return NeedWorkerImages, nil
	case *core.PlacementPolicyConfig_BoundedLoads:
		// Normalized worker load average drives the CH-BL ring.
		return NeedWorkerLoad, nil
	default:
		return 0, fmt.Errorf("placement policy is not set")
	}
}
