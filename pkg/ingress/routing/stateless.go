package routing

import "sync/atomic"

// randomPolicy selects a healthy leaf uniformly. It is a stateless baseline:
// its model reads only healthy membership, so its subscriber requests no scalar
// and no capacity.
type randomPolicy struct{ seed uint64 }

func (randomPolicy) Needs() RoutingNeeds { return 0 }
func (p randomPolicy) NewModel(t Topology) routingModel {
	return &randomModel{membership: newMembership(t), seed: p.seed}
}

type randomModel struct {
	membership
	seed uint64
}

func (m *randomModel) ReplaceLeaf(s LeafState) UpdateResult {
	if m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0) {
		return PublishPicker
	}
	return NoChange
}
func (m *randomModel) Apply(s LeafState) UpdateResult { return m.ReplaceLeaf(s) }
func (m *randomModel) LeafDisconnected(id uint64) UpdateResult {
	return m.ReplaceLeaf(LeafState{LeafID: id})
}
func (m *randomModel) Picker() Picker {
	return &randomPicker{leaves: m.healthyLeaves(), seed: m.seed}
}

type randomPicker struct {
	leaves  []LeafTarget
	seed    uint64
	counter atomic.Uint64
}

func (p *randomPicker) Pick(_ RouteRequest) (LeafTarget, error) {
	n := uint64(len(p.leaves))
	if n == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	idx := hashFunctionID(p.counter.Add(1)^p.seed) % n
	target := p.leaves[idx]
	target.Reason = ReasonRandom
	return target, nil
}

// roundRobinPolicy cycles through healthy leaves. It is a stateless baseline:
// its model reads only healthy membership.
type roundRobinPolicy struct{}

func (roundRobinPolicy) Needs() RoutingNeeds { return 0 }
func (roundRobinPolicy) NewModel(t Topology) routingModel {
	return &roundRobinModel{membership: newMembership(t)}
}

type roundRobinModel struct {
	membership
}

func (m *roundRobinModel) ReplaceLeaf(s LeafState) UpdateResult {
	if m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0) {
		return PublishPicker
	}
	return NoChange
}
func (m *roundRobinModel) Apply(s LeafState) UpdateResult { return m.ReplaceLeaf(s) }
func (m *roundRobinModel) LeafDisconnected(id uint64) UpdateResult {
	return m.ReplaceLeaf(LeafState{LeafID: id})
}
func (m *roundRobinModel) Picker() Picker {
	return &roundRobinPicker{leaves: m.healthyLeaves()}
}

type roundRobinPicker struct {
	leaves  []LeafTarget
	counter atomic.Uint64
}

func (p *roundRobinPicker) Pick(_ RouteRequest) (LeafTarget, error) {
	n := uint64(len(p.leaves))
	if n == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	target := p.leaves[int(p.counter.Add(1)-1)%len(p.leaves)]
	target.Reason = ReasonRoundRobin
	return target, nil
}
