package scheduler

import (
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

func placementCfg(policy *core.PlacementPolicyConfig) *core.PlacementPolicyConfig { return policy }

func balancedPlacement() *core.PlacementPolicyConfig {
	return &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BalancedRoundRobin{BalancedRoundRobin: &core.BalancedRoundRobinPlacement{}}}
}

func TestNeedsForEachPlacementPolicy(t *testing.T) {
	cases := []struct {
		name string
		cfg  *core.PlacementPolicyConfig
		want PlacementNeeds
	}{
		{"balanced-round-robin", balancedPlacement(), 0},
		{"cold-start-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ColdStartAware{ColdStartAware: &core.ColdStartAwarePlacement{}}}, 0},
		{"reservation-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ReservationAware{ReservationAware: &core.ReservationAwarePlacement{}}}, 0},
		{"resource-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ResourceAware{ResourceAware: &core.ResourceAwarePlacement{}}}, 0},
		{"image-aware", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_ImageAware{ImageAware: &core.ImageAwarePlacement{}}}, NeedWorkerImages},
		{"bounded-loads", &core.PlacementPolicyConfig{Policy: &core.PlacementPolicyConfig_BoundedLoads{BoundedLoads: &core.BoundedLoadsPlacement{Bound: 1.5, MaxChainLen: 3}}}, NeedWorkerLoad},
	}
	for _, tc := range cases {
		got, err := NeedsFor(tc.cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s needs = %b, want %b", tc.name, got, tc.want)
		}
	}
}

func TestNeedsForRejectsMissingPolicy(t *testing.T) {
	if _, err := NeedsFor(nil); err == nil {
		t.Fatal("expected an error for a missing placement policy")
	}
	if _, err := NeedsFor(placementCfg(&core.PlacementPolicyConfig{})); err == nil {
		t.Fatal("expected an error for a placement policy with no oneof set")
	}
}

func TestNewFromConfigBuildsOneSchedulerPerPolicy(t *testing.T) {
	sched, err := NewFromConfig(balancedPlacement(), 2, 4)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if _, ok := sched.(*BalancedRoundRobin); !ok {
		t.Fatalf("got %T, want *BalancedRoundRobin", sched)
	}
	if _, err := NewFromConfig(nil, 2, 4); err == nil {
		t.Fatal("expected an error for a nil placement policy")
	}
}
