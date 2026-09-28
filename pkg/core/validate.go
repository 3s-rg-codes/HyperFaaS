package core

import "fmt"

// ValidatePlatformConfig checks a dynamic platform-config document before it is
// stored by the control plane or applied by a component.
//
// It rejects documents that select no policy, or that carry invalid policy
// settings, so that a bad document cannot silently disable a component or
// install a policy with nonsensical bounds. Zero-valued knobs are allowed and
// mean "use the built-in default".
func ValidatePlatformConfig(cfg *PlatformConfig) error {
	if cfg == nil {
		return fmt.Errorf("platform config is required")
	}
	if cfg.GetRouting() == nil || cfg.GetRouting().GetPolicy() == nil {
		return fmt.Errorf("platform config routing policy is required")
	}
	if bl := cfg.GetRouting().GetBoundedLoads(); bl != nil {
		if bl.GetBound() < 0 {
			return fmt.Errorf("routing bounded-loads bound must be >= 0")
		}
		if bl.GetMaxChainLen() < 0 {
			return fmt.Errorf("routing bounded-loads max_chain_len must be >= 0")
		}
	}
	if cfg.GetPlacement() == nil || cfg.GetPlacement().GetPolicy() == nil {
		return fmt.Errorf("platform config placement policy is required")
	}
	if bl := cfg.GetPlacement().GetBoundedLoads(); bl != nil {
		if bl.GetBound() < 0 {
			return fmt.Errorf("placement bounded-loads bound must be >= 0")
		}
		if bl.GetMaxChainLen() < 0 {
			return fmt.Errorf("placement bounded-loads max_chain_len must be >= 0")
		}
	}
	if cfg.GetStateRefreshInterval() != nil && cfg.GetStateRefreshInterval().AsDuration() < 0 {
		return fmt.Errorf("platform config state_refresh_interval must be >= 0")
	}
	return nil
}
