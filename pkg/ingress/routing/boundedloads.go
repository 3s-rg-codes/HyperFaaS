package routing

import (
	"math"
	"sync/atomic"

	"hyperfaas-ideal-arch/pkg/chbl"
)

// boundedLoadsPolicy uses a ring over healthy leaves and one load per leaf.
// Load updates touch one atomic slot; a health change rebuilds the ring.
type boundedLoadsPolicy struct {
	bound       float64
	maxChainLen int
}

func (boundedLoadsPolicy) Needs() RoutingNeeds { return NeedLeafLoad }
func (p boundedLoadsPolicy) NewModel(t Topology) routingModel {
	return &boundedLoadsModel{
		membership:  newMembership(t),
		template:    chbl.NewRing(t.IDs()),
		loads:       make([]atomic.Uint64, len(t.leaves)),
		bound:       chbl.NormalizeBound(p.bound),
		maxChainLen: chbl.NormalizeMaxChainLen(p.maxChainLen),
	}
}

type boundedLoadsModel struct {
	membership
	loads       []atomic.Uint64 // float64 bits; the slice never grows
	template    *chbl.Ring
	ring        *chbl.Ring
	dirty       bool
	bound       float64
	maxChainLen int
}

func (m *boundedLoadsModel) ReplaceLeaf(s LeafState) UpdateResult {
	healthChanged := m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
	if healthChanged {
		m.dirty = true
	}
	valueChanged := false
	if s.FullSnapshot || s.HasLeafLoad {
		if i, ok := m.topology.index[s.LeafID]; ok {
			bits := math.Float64bits(s.LeafLoad)
			if m.loads[i].Load() != bits {
				m.loads[i].Store(bits)
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

func (m *boundedLoadsModel) Apply(s LeafState) UpdateResult { return m.ReplaceLeaf(s) }

func (m *boundedLoadsModel) LeafDisconnected(id uint64) UpdateResult {
	if m.membership.setHealthy(id, false) {
		m.dirty = true
		return PublishPicker
	}
	return NoChange
}

func (m *boundedLoadsModel) Picker() Picker {
	leaves := m.healthyLeaves()
	if m.ring == nil || m.dirty {
		m.ring = m.template.Subset(m.healthy)
		m.dirty = false
	}
	byID := make(map[uint64]LeafTarget, len(leaves))
	for _, leaf := range leaves {
		byID[leaf.LeafID] = leaf
	}
	ringIDs := m.ring.IDs()
	ringSlots := make([]int, len(ringIDs))
	for i, id := range ringIDs {
		ringSlots[i] = m.topology.index[id]
	}
	return &boundedLoadsPicker{
		ring:        m.ring,
		ringIDs:     ringIDs,
		ringSlots:   ringSlots,
		leaves:      leaves,
		byID:        byID,
		index:       m.topology.index,
		loads:       m.loads,
		bound:       m.bound,
		maxChainLen: m.maxChainLen,
	}
}

type boundedLoadsPicker struct {
	ring        *chbl.Ring
	ringIDs     []uint64 // ring.IDs()
	ringSlots   []int    // load slot of each ringIDs entry, for the full scan
	leaves      []LeafTarget
	byID        map[uint64]LeafTarget
	index       map[uint64]int // static topology index
	loads       []atomic.Uint64
	bound       float64
	maxChainLen int
}

func (p *boundedLoadsPicker) Load(id uint64) float64 {
	return math.Float64frombits(p.loads[p.index[id]].Load())
}

// LeastLoadedID scans the ring members through precomputed slots. It matches
// chbl.LeastLoadedBy(p.ring.IDs(), p): lowest load, ties to the lowest ID.
func (p *boundedLoadsPicker) LeastLoadedID() uint64 {
	if len(p.ringIDs) == 0 {
		return 0
	}
	best := p.ringIDs[0]
	bestLoad := math.Float64frombits(p.loads[p.ringSlots[0]].Load())
	for i := 1; i < len(p.ringIDs); i++ {
		load := math.Float64frombits(p.loads[p.ringSlots[i]].Load())
		if id := p.ringIDs[i]; load < bestLoad || (load == bestLoad && id < best) {
			best = id
			bestLoad = load
		}
	}
	return best
}

func (p *boundedLoadsPicker) Pick(req RouteRequest) (LeafTarget, error) {
	if p.ring == nil || len(p.leaves) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	home := p.ring.Home(req.FunctionID)
	leafID, reason := chbl.ForwardBy(p.ring, p, p.bound, p.maxChainLen, home)
	if target, ok := p.byID[leafID]; ok {
		target.Reason = boundedLoadsReason(reason)
		return target, nil
	}
	target := p.leaves[0]
	target.Reason = boundedLoadsReason(reason)
	return target, nil
}

func boundedLoadsReason(reason string) Reason {
	switch reason {
	case chbl.ReasonHome:
		return ReasonBoundedLoadsHome
	case chbl.ReasonForwarded:
		return ReasonBoundedLoadsForwarded
	default:
		return ReasonBoundedLoadsLeastLoaded
	}
}
