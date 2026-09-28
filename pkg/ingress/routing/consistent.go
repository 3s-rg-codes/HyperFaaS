package routing

import "hyperfaas-ideal-arch/pkg/chbl"

// consistentHashingPolicy maps each function to a home leaf on a hash ring. It
// reads only healthy membership.
type consistentHashingPolicy struct{}

func (consistentHashingPolicy) Needs() RoutingNeeds { return 0 }
func (consistentHashingPolicy) NewModel(t Topology) routingModel {
	return &consistentModel{membership: newMembership(t), template: chbl.NewRing(t.IDs())}
}

type consistentModel struct {
	membership
	template *chbl.Ring
	ring     *chbl.Ring
	dirty    bool
}

func (m *consistentModel) ReplaceLeaf(s LeafState) UpdateResult {
	changed := m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
	if changed {
		m.dirty = true
		return PublishPicker
	}
	return NoChange
}
func (m *consistentModel) Apply(s LeafState) UpdateResult { return m.ReplaceLeaf(s) }
func (m *consistentModel) LeafDisconnected(id uint64) UpdateResult {
	return m.ReplaceLeaf(LeafState{LeafID: id})
}

func (m *consistentModel) Picker() Picker {
	leaves := m.healthyLeaves()
	if m.ring == nil || m.dirty {
		// The ring contains only healthy leaves, so a leaf becoming unhealthy or
		// recovering remaps the affected keys. Subset reuses the configured
		// ring positions instead of hashing and sorting them again.
		m.ring = m.template.Subset(m.healthy)
		m.dirty = false
	}
	byID := make(map[uint64]LeafTarget, len(leaves))
	order := make([]uint64, len(leaves))
	for i, l := range leaves {
		byID[l.LeafID] = l
		order[i] = l.LeafID
	}
	return &consistentPicker{ring: m.ring, byID: byID, order: order}
}

type consistentPicker struct {
	ring  *chbl.Ring
	byID  map[uint64]LeafTarget
	order []uint64
}

func (p *consistentPicker) Pick(req RouteRequest) (LeafTarget, error) {
	if p.ring == nil || len(p.byID) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	home := p.ring.Home(req.FunctionID)
	if target, ok := p.byID[home]; ok {
		target.Reason = ReasonConsistentHashingHome
		return target, nil
	}
	for _, id := range p.order {
		target := p.byID[id]
		target.Reason = ReasonConsistentHashingHome
		return target, nil
	}
	return LeafTarget{}, noHealthyLeaves()
}
