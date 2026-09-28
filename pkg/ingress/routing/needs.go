package routing

import (
	"hyperfaas-ideal-arch/pkg/core"
)

// RoutingNeeds is the set of routing-state components a policy reads.
//
// It is the routing-domain contract, separate from PlacementNeeds because the
// fields and meanings differ. It drives the projection a subscriber requests
// from each leaf, so a policy only pays for the state it reads.
type RoutingNeeds uint32

const (
	// NeedLeafLoad is the per-leaf scalar leaf_load (max normalized worker
	// load). Used by bounded-loads, RJ-CH, and CH-RLU.
	NeedLeafLoad RoutingNeeds = 1 << iota
	// NeedAggregateInFlight is the per-leaf total in-flight count. Used by
	// least-loaded so ingress never receives per-function capacity just to sum
	// it.
	NeedAggregateInFlight
	// NeedFunctionCapacity is the per-function capacity entries. Used by
	// available-capacity.
	NeedFunctionCapacity
)

// Has reports whether every bit in x is set in n.
func (n RoutingNeeds) Has(x RoutingNeeds) bool { return n&x == x }

// NeedsFor returns the routing state a configured policy reads.
//
// Healthy membership and the per-leaf revision are carried by every frame and
// are not part of this set.
func NeedsFor(cfg *core.RoutingPolicyConfig) (RoutingNeeds, error) {
	policy, err := newRoutingPolicy(cfg)
	if err != nil {
		return 0, err
	}
	return policy.Needs(), nil
}
