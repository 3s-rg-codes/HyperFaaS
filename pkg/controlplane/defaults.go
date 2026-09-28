package controlplane

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"hyperfaas-ideal-arch/pkg/core"
)

// DefaultPlatformConfig is the policy document a fresh deployment starts with.
//
// The control plane seeds it once if the store has no document, so every
// component always receives a configuration and no component needs a static
// YAML policy to serve. The seed matches the platform defaults: warm-aware
// routing, balanced placement, and a 500 ms state refresh so scalar signals do
// not go stale between capacity changes.
func DefaultPlatformConfig() *core.PlatformConfig {
	return &core.PlatformConfig{
		Routing: &core.RoutingPolicyConfig{
			Policy: &core.RoutingPolicyConfig_AvailableCapacity{
				AvailableCapacity: &core.AvailableCapacityRoutingPolicy{},
			},
		},
		Placement: &core.PlacementPolicyConfig{
			Policy: &core.PlacementPolicyConfig_BalancedRoundRobin{
				BalancedRoundRobin: &core.BalancedRoundRobinPlacement{},
			},
		},
		StateRefreshInterval: durationpb.New(500 * time.Millisecond),
	}
}
