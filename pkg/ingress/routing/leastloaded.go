package routing

import "sync/atomic"

// leastLoadedPolicy reads one aggregate count per leaf. A scalar update changes
// one atomic slot; only a health change rebuilds the candidate list.
type leastLoadedPolicy struct{}

func (leastLoadedPolicy) Needs() RoutingNeeds { return NeedAggregateInFlight }
func (leastLoadedPolicy) NewModel(t Topology) routingModel {
	return &leastLoadedModel{
		membership: newMembership(t),
		inFlight:   make([]atomic.Uint64, len(t.leaves)),
	}
}

type leastLoadedModel struct {
	membership
	inFlight []atomic.Uint64 // fixed-size; readers load slots through the picker
}

func (m *leastLoadedModel) ReplaceLeaf(s LeafState) UpdateResult {
	healthChanged := m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
	valueChanged := false
	if s.FullSnapshot || s.HasAggregateInFlight {
		if i, ok := m.topology.index[s.LeafID]; ok {
			if m.inFlight[i].Load() != s.AggregateInFlight {
				m.inFlight[i].Store(s.AggregateInFlight)
				valueChanged = true
			}
		}
	}
	if healthChanged {
		return PublishPicker
	}
	if valueChanged {
		return ChangedInPlace
	}
	return NoChange
}

func (m *leastLoadedModel) Apply(s LeafState) UpdateResult { return m.ReplaceLeaf(s) }

func (m *leastLoadedModel) LeafDisconnected(id uint64) UpdateResult {
	if m.membership.setHealthy(id, false) {
		return PublishPicker
	}
	return NoChange
}

func (m *leastLoadedModel) Picker() Picker {
	leaves := m.healthyLeaves()
	slots := make([]int, len(leaves))
	for i, leaf := range leaves {
		slots[i] = m.topology.index[leaf.LeafID]
	}
	return &leastLoadedPicker{leaves: leaves, slots: slots, inFlight: m.inFlight}
}

type leastLoadedPicker struct {
	leaves   []LeafTarget
	slots    []int
	inFlight []atomic.Uint64
}

func (p *leastLoadedPicker) Pick(_ RouteRequest) (LeafTarget, error) {
	if len(p.leaves) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	best := 0
	bestLoad := p.inFlight[p.slots[0]].Load()
	for i := 1; i < len(p.leaves); i++ {
		load := p.inFlight[p.slots[i]].Load()
		// Leaves are sorted by ID, so keeping the first equal load
		// preserves the lowest-ID tie break.
		if load < bestLoad {
			best = i
			bestLoad = load
		}
	}
	target := p.leaves[best]
	target.Reason = ReasonLeastLoaded
	return target, nil
}
