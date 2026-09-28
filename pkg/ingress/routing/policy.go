package routing

import (
	"hyperfaas-ideal-arch/pkg/core"
)

// Stable policy names. They match the field names of the routing-policy oneof.
const (
	PolicyRandom            = "random"
	PolicyRoundRobin        = "round-robin"
	PolicyConsistentHashing = "consistent-hashing"
	PolicyBoundedLoads      = "bounded-loads"
	PolicyRJCH              = "rj-ch"
	PolicyCHRLU             = "ch-rlu"
	PolicyLeastLoaded       = "least-loaded"
	PolicyAvailableCapacity = "available-capacity"
)

// RoutingPolicyName returns the stable name of a configured routing policy.
func RoutingPolicyName(cfg *core.RoutingPolicyConfig) string {
	switch cfg.GetPolicy().(type) {
	case *core.RoutingPolicyConfig_Random:
		return PolicyRandom
	case *core.RoutingPolicyConfig_RoundRobin:
		return PolicyRoundRobin
	case *core.RoutingPolicyConfig_ConsistentHashing:
		return PolicyConsistentHashing
	case *core.RoutingPolicyConfig_BoundedLoads:
		return PolicyBoundedLoads
	case *core.RoutingPolicyConfig_RjCh:
		return PolicyRJCH
	case *core.RoutingPolicyConfig_ChRlu:
		return PolicyCHRLU
	case *core.RoutingPolicyConfig_LeastLoaded:
		return PolicyLeastLoaded
	case *core.RoutingPolicyConfig_AvailableCapacity:
		return PolicyAvailableCapacity
	default:
		return ""
	}
}
