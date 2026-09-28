package routing

import (
	"fmt"

	"hyperfaas-ideal-arch/pkg/core"
)

// LeafState is one leaf's routing state, translated out of the wire frame so
// policy models do not depend on the leaf proto. A model that retains a field
// from Capacities must copy it; the frame owns those protobuf messages.
type LeafState struct {
	ConfigVersion  uint64
	LeafID         uint64
	Revision       uint64
	FullSnapshot   bool
	HealthyWorkers uint32

	LeafLoad    float64
	HasLeafLoad bool

	AggregateInFlight    uint64
	HasAggregateInFlight bool

	// Capacities is nil unless the policy requested function capacity.
	Capacities []*core.FunctionCapacity
}

// UpdateResult tells the controller whether a policy needs a new root picker.
// The policy owns all state maintenance. In particular, ChangedInPlace means
// that it has already made the update safe for concurrent Pick calls.
type UpdateResult uint8

const (
	// NoChange means that no request-visible value changed.
	NoChange UpdateResult = iota
	// ChangedInPlace means the model has published its own safe value update.
	ChangedInPlace
	// PublishPicker asks the controller to install a new root picker.
	PublishPicker
)

// routingModel owns one policy's writer-side state and request-side layout.
// The controller calls its update methods under one mutex. A policy may keep
// private maps, cached metrics, or indexes and may publish immutable copies or
// atomic values to its picker. It must never let Pick read a mutable map or
// slice without synchronization.
type routingModel interface {
	ReplaceLeaf(LeafState) UpdateResult
	Apply(LeafState) UpdateResult
	// LeafDisconnected changes only stream health. It does not invent values
	// for projected metrics when a stream ends.
	LeafDisconnected(uint64) UpdateResult
	// Picker builds a new root when first activated or after PublishPicker.
	// Its structure is immutable; it may refer to policy-owned atomic values.
	Picker() Picker
}

// routingPolicy declares the leaf signals a policy reads and creates its model.
// A new policy implements this interface and routingModel, then adds its config
// case to newRoutingPolicy. The model may use any private data layout. The
// controller needs no new case for a policy using existing leaf signals.
type routingPolicy interface {
	Needs() RoutingNeeds
	NewModel(Topology) routingModel
}

// newRoutingPolicy maps a configured oneof policy to its implementation.
func newRoutingPolicy(cfg *core.RoutingPolicyConfig) (routingPolicy, error) {
	switch p := cfg.GetPolicy().(type) {
	case *core.RoutingPolicyConfig_Random:
		return randomPolicy{seed: 1}, nil
	case *core.RoutingPolicyConfig_RoundRobin:
		return roundRobinPolicy{}, nil
	case *core.RoutingPolicyConfig_ConsistentHashing:
		return consistentHashingPolicy{}, nil
	case *core.RoutingPolicyConfig_BoundedLoads:
		return boundedLoadsPolicy{
			bound:       p.BoundedLoads.GetBound(),
			maxChainLen: int(p.BoundedLoads.GetMaxChainLen()),
		}, nil
	case *core.RoutingPolicyConfig_RjCh:
		return randomJumpPolicy{bound: p.RjCh.GetBound(), maxJumps: int(p.RjCh.GetMaxJumps())}, nil
	case *core.RoutingPolicyConfig_ChRlu:
		latencies := make(map[uint64]latencyEstimate, len(p.ChRlu.GetLatencyEstimates()))
		for _, estimate := range p.ChRlu.GetLatencyEstimates() {
			if estimate.GetColdTimeMs() > 0 && estimate.GetWarmTimeMs() > 0 {
				latencies[estimate.GetFunctionId()] = latencyEstimate{coldMS: estimate.GetColdTimeMs(), warmMS: estimate.GetWarmTimeMs()}
			}
		}
		return chrluPolicy{
			bound: p.ChRlu.GetBound(), maxChainLen: int(p.ChRlu.GetMaxChainLen()),
			maxBound: p.ChRlu.GetMaxBound(), coldTimeMS: p.ChRlu.GetColdTimeMs(),
			warmTimeMS: p.ChRlu.GetWarmTimeMs(), samplePercent: p.ChRlu.GetSamplePercent(),
			popularIATMS: p.ChRlu.GetPopularIatMs(), noiseStddev: p.ChRlu.GetNoiseStddev(),
			heartbeatSeconds: p.ChRlu.GetHeartbeatSeconds(), latencies: latencies,
		}, nil
	case *core.RoutingPolicyConfig_LeastLoaded:
		return leastLoadedPolicy{}, nil
	case *core.RoutingPolicyConfig_AvailableCapacity:
		return availableCapacityPolicy{}, nil
	default:
		return nil, fmt.Errorf("routing policy is not set")
	}
}
