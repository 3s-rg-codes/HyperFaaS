package autoscaling

import (
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/leaf"
)

func TestNewPolicyPassesConfiguredAutoscalingKnobs(t *testing.T) {
	policy := NewPolicy(leaf.LeafConfig{Autoscaling: leaf.AutoscalingConfig{
		StableWindow:        time.Minute,
		PanicWindow:         6 * time.Second,
		PanicThresholdRatio: 2,
		MaxScaleUpRate:      1000,
		MaxScaleDownRate:    2,
	}})
	got, ok := policy.(*DefaultPolicy)
	if !ok {
		t.Fatalf("policy type %T, want *DefaultPolicy", policy)
	}
	if got.StableWindow != time.Minute || got.PanicWindow != 6*time.Second ||
		got.PanicThresholdRatio != 2 || got.MaxScaleUpRate != 1000 || got.MaxScaleDownRate != 2 {
		t.Fatalf("factory did not pass autoscaling knobs: %+v", got)
	}
}
