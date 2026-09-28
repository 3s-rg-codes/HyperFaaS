package shared

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/core"
)

// Platform policy helpers for the DST suite.
//
// Routing and placement policies are global and live in the
// control-plane PlatformConfig document. Tests publish the policy they need
// through the admin API before their body runs instead of baking it into
// leaf/ingress YAML or a per-function field.

// AvailableCapacityRouting is the platform default: warm-aware leaf routing.
func AvailableCapacityRouting() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_AvailableCapacity{AvailableCapacity: &core.AvailableCapacityRoutingPolicy{}}}
}

// ConsistentHashingRouting pins each function to a home leaf.
func ConsistentHashingRouting() *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_ConsistentHashing{ConsistentHashing: &core.ConsistentHashingRoutingPolicy{}}}
}

// BoundedLoadsRouting is ingress consistent hashing with bounded loads.
func BoundedLoadsRouting(bound float64, maxChainLen int32) *core.RoutingPolicyConfig {
	return &core.RoutingPolicyConfig{Policy: &core.RoutingPolicyConfig_BoundedLoads{BoundedLoads: &core.BoundedLoadsRoutingPolicy{Bound: bound, MaxChainLen: maxChainLen}}}
}

// BalancedRoundRobinPlacement is the platform default leaf-local placement.
func BalancedRoundRobinPlacement() *core.PlacementPolicyConfig {
	return &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BalancedRoundRobin{BalancedRoundRobin: &core.BalancedRoundRobinPlacement{}}}
}

// ImageAwarePlacement prefers workers that already advertise the function image.
func ImageAwarePlacement() *core.PlacementPolicyConfig {
	return &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ImageAware{ImageAware: &core.ImageAwarePlacement{}}}
}

// ApplyPlatformConfig publishes a complete platform-config document and returns
// the version the control plane assigned. Both a routing and a placement policy
// are required by validation.
//
// The control plane stores the document and streams it to ingress and leaves
// through their watches; the helper waits briefly for that propagation before
// returning. It registers a cleanup that restores the platform default so later
// tests start from a known policy.
func (h *Harness) ApplyPlatformConfig(ctx context.Context, routing *core.RoutingPolicyConfig, placement *core.PlacementPolicyConfig) uint64 {
	h.T.Helper()
	stored, err := h.CP.PutPlatformConfig(ctx, &core.PlatformConfig{
		Routing:              routing,
		Placement:            placement,
		StateRefreshInterval: durationpb.New(500 * time.Millisecond),
	}, 0)
	if err != nil {
		h.T.Fatalf("put platform config: %v", err)
	}
	if stored.GetVersion() == 0 {
		h.T.Fatal("control plane returned platform config version 0")
	}
	h.Log.Info("applied platform config", "config_version", stored.GetVersion())
	// Watches deliver the document asynchronously. There is no applied-version
	// endpoint on ingress, so allow a short, bounded propagation window.
	time.Sleep(500 * time.Millisecond)
	h.T.Cleanup(func() {
		if _, err := h.CP.PutPlatformConfig(context.Background(), controlplane.DefaultPlatformConfig(), 0); err != nil {
			h.Log.Warn("failed to restore default platform config", "error", err)
		}
	})
	return stored.GetVersion()
}
