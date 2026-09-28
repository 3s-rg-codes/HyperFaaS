package routing

// TODO: missing citation
// randomJumpPolicy keeps the CH home, then probes independent hash positions
// instead of forwarding along successive ring neighbors.
type randomJumpPolicy struct {
	bound    float64
	maxJumps int
}

func (randomJumpPolicy) Needs() RoutingNeeds { return NeedLeafLoad }

func (p randomJumpPolicy) NewModel(t Topology) routingModel {
	base := boundedLoadsPolicy{bound: p.bound, maxChainLen: p.maxJumps}.NewModel(t).(*boundedLoadsModel)
	return &randomJumpModel{boundedLoadsModel: base}
}

// The embedded model owns membership and atomic load slots. Its update methods
// retain bounded-loads' NoChange/ChangedInPlace/PublishPicker contract.
type randomJumpModel struct{ *boundedLoadsModel }

func (m *randomJumpModel) ReplaceLeaf(s LeafState) UpdateResult {
	return m.boundedLoadsModel.ReplaceLeaf(s)
}
func (m *randomJumpModel) Apply(s LeafState) UpdateResult { return m.boundedLoadsModel.Apply(s) }
func (m *randomJumpModel) LeafDisconnected(id uint64) UpdateResult {
	return m.boundedLoadsModel.LeafDisconnected(id)
}

func (m *randomJumpModel) Picker() Picker {
	return &randomJumpPicker{base: m.boundedLoadsModel.Picker().(*boundedLoadsPicker)}
}

type randomJumpPicker struct{ base *boundedLoadsPicker }

func (p *randomJumpPicker) Pick(req RouteRequest) (LeafTarget, error) {
	b := p.base
	if b.ring == nil || len(b.leaves) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	home := b.ring.Home(req.FunctionID)
	id, reason := home, ReasonRJCHHome
	if b.Load(home) >= b.bound {
		// Hash the function together with the failed attempt. Each jump is
		// independent of the full leaf's position, avoiding cascaded overflow.
		reason = ReasonRJCHJump
		for attempt := 1; attempt <= b.maxChainLen; attempt++ {
			candidate := b.leaves[hashFunctionID(req.FunctionID^uint64(attempt)*0x9e3779b97f4a7c15)%uint64(len(b.leaves))].LeafID
			if b.Load(candidate) < b.bound {
				id = candidate
				break
			}
		}
		if id == home {
			id = b.LeastLoadedID()
			reason = ReasonRJCHLeastLoaded
		}
	}
	target := b.byID[id]
	target.Reason = reason
	return target, nil
}
