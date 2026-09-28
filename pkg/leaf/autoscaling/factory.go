package autoscaling

import (
	"hyperfaas-ideal-arch/pkg/leaf"
)

// NewPolicy builds the leaf autoscaling policy.
//
// The autoscaling strategy is fixed in code; only its knobs are configurable.
// There is deliberately no policy selection here: swapping the strategy would
// complicate the leaf reconciliation path without adding a meaningful benchmark
// dimension. See docs/DYNAMIC_POLICY_STATE_GATING_DESIGN.md.
func NewPolicy(cfg leaf.LeafConfig) Policy {
	return &DefaultPolicy{
		ScaleToZeroAfter:           cfg.Dataplane.ScaleToZeroAfter,
		UnlimitedConcurrencyTarget: cfg.Autoscaling.UnlimitedConcurrencyTarget,
		TargetUtilization:          cfg.Autoscaling.TargetUtilization,
		ColdStartPanicScaling:      cfg.Autoscaling.ColdStartPanicScaling,
		StableWindow:               cfg.Autoscaling.StableWindow,
		PanicWindow:                cfg.Autoscaling.PanicWindow,
		PanicThresholdRatio:        cfg.Autoscaling.PanicThresholdRatio,
		MaxScaleUpRate:             cfg.Autoscaling.MaxScaleUpRate,
		MaxScaleDownRate:           cfg.Autoscaling.MaxScaleDownRate,
		DirigentAdmission:          cfg.Dataplane.DirigentStrictAdmission,
	}
}
