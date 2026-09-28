package routing

import (
	"sync/atomic"
)

// availableCapacityPolicy keeps one route list per function. A capacity frame
// copies only lists for functions changed by that frame.
type availableCapacityPolicy struct{}

func (availableCapacityPolicy) Needs() RoutingNeeds { return NeedFunctionCapacity }
func (availableCapacityPolicy) NewModel(t Topology) routingModel {
	return &availableCapacityModel{
		membership: newMembership(t),
		byLeaf:     make(map[uint64]map[uint64]struct{}),
		cells:      make(map[uint64]*capacityCell),
	}
}

// capacityRoute uses a topology slot rather than copying the leaf address into
// every function list. The slice has no pointers for the GC to scan.
type capacityRoute struct {
	leaf      int
	available uint64
}

type capacityRoutes struct{ entries []capacityRoute }

var emptyCapacityRoutes = capacityRoutes{}

// capacityCell has a writer-only sorted slice and one published immutable
// copy. A request reads only pub; the controller is the sole writer of parts.
type capacityCell struct {
	fn    uint64
	parts []capacityRoute
	pub   atomic.Pointer[capacityRoutes]
	dirty bool
}

type capacityTable map[uint64]*capacityCell

type availableCapacityModel struct {
	membership
	// byLeaf records advertised function IDs so a full snapshot can remove
	// functions missing from the new baseline.
	byLeaf map[uint64]map[uint64]struct{}
	cells  capacityTable // writer-only function map
	pub    atomic.Pointer[capacityTable]

	dirtyCells []*capacityCell // reused scratch space, never read by Pick
	keysDirty  bool
	active     bool // set when the first picker is built
}

func (m *availableCapacityModel) ReplaceLeaf(s LeafState) UpdateResult {
	healthChanged := m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
	leaf, ok := m.topology.index[s.LeafID]
	if !ok {
		if healthChanged {
			return PublishPicker
		}
		return NoChange
	}
	previous := m.byLeaf[s.LeafID]
	next := make(map[uint64]struct{}, len(s.Capacities))
	for _, capacity := range s.Capacities {
		if capacity.GetDeleted() {
			continue
		}
		fn := capacity.GetFunctionId()
		next[fn] = struct{}{}
		m.upsert(fn, leaf, capacity.GetAvailableConcurrency())
	}
	for fn := range previous {
		if _, present := next[fn]; !present {
			m.remove(fn, leaf)
		}
	}
	m.byLeaf[s.LeafID] = next
	if !m.active {
		// Baselines can contain many leaves. Publish their combined index
		// once when the controller builds the first picker.
		return NoChange
	}
	changed := m.commit()
	if healthChanged {
		return PublishPicker
	}
	if changed {
		return ChangedInPlace
	}
	return NoChange
}

func (m *availableCapacityModel) Apply(s LeafState) UpdateResult {
	healthChanged := m.membership.setHealthy(s.LeafID, s.HealthyWorkers > 0)
	leaf, ok := m.topology.index[s.LeafID]
	if !ok {
		if healthChanged {
			return PublishPicker
		}
		return NoChange
	}
	if len(s.Capacities) != 0 && m.byLeaf[s.LeafID] == nil {
		m.byLeaf[s.LeafID] = make(map[uint64]struct{}, len(s.Capacities))
	}
	for _, capacity := range s.Capacities {
		fn := capacity.GetFunctionId()
		if capacity.GetDeleted() {
			delete(m.byLeaf[s.LeafID], fn)
			m.remove(fn, leaf)
			continue
		}
		m.byLeaf[s.LeafID][fn] = struct{}{}
		m.upsert(fn, leaf, capacity.GetAvailableConcurrency())
	}
	if !m.active {
		return NoChange
	}
	changed := m.commit()
	if healthChanged {
		return PublishPicker
	}
	if changed {
		return ChangedInPlace
	}
	return NoChange
}

func (m *availableCapacityModel) LeafDisconnected(id uint64) UpdateResult {
	if m.membership.setHealthy(id, false) {
		return PublishPicker
	}
	return NoChange
}

// routeSlot returns the position of leaf in the sorted writer slice.
func routeSlot(routes []capacityRoute, leaf int) int {
	lo, hi := 0, len(routes)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if routes[mid].leaf < leaf {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (m *availableCapacityModel) touch(cell *capacityCell) {
	if !cell.dirty {
		cell.dirty = true
		m.dirtyCells = append(m.dirtyCells, cell)
	}
}

func (m *availableCapacityModel) upsert(fn uint64, leaf int, available uint64) {
	cell := m.cells[fn]
	if cell == nil {
		cell = &capacityCell{fn: fn}
		m.cells[fn] = cell
		m.keysDirty = true
	}
	i := routeSlot(cell.parts, leaf)
	if i < len(cell.parts) && cell.parts[i].leaf == leaf {
		if cell.parts[i].available == available {
			return
		}
		cell.parts[i].available = available
	} else {
		cell.parts = append(cell.parts, capacityRoute{})
		copy(cell.parts[i+1:], cell.parts[i:])
		cell.parts[i] = capacityRoute{leaf: leaf, available: available}
	}
	m.touch(cell)
}

func (m *availableCapacityModel) remove(fn uint64, leaf int) {
	cell := m.cells[fn]
	if cell == nil {
		return
	}
	i := routeSlot(cell.parts, leaf)
	if i == len(cell.parts) || cell.parts[i].leaf != leaf {
		return
	}
	copy(cell.parts[i:], cell.parts[i+1:])
	cell.parts = cell.parts[:len(cell.parts)-1]
	m.touch(cell)
}

// commit publishes each touched function once, even when a full snapshot or
// delta contains several changes to that function. It copies the outer map
// only when a function enters or leaves the set of advertised functions.
func (m *availableCapacityModel) commit() bool {
	if len(m.dirtyCells) == 0 {
		return false
	}
	for i, cell := range m.dirtyCells {
		if len(cell.parts) == 0 {
			cell.pub.Store(&emptyCapacityRoutes)
			delete(m.cells, cell.fn)
			m.keysDirty = true
		} else {
			copyOfParts := append([]capacityRoute(nil), cell.parts...)
			cell.pub.Store(&capacityRoutes{entries: copyOfParts})
		}
		cell.dirty = false
		m.dirtyCells[i] = nil
	}
	m.dirtyCells = m.dirtyCells[:0]
	if m.keysDirty {
		table := make(capacityTable, len(m.cells))
		for fn, cell := range m.cells {
			table[fn] = cell
		}
		m.pub.Store(&table)
		m.keysDirty = false
	}
	return true
}

func (m *availableCapacityModel) Picker() Picker {
	m.commit()
	m.active = true
	healthy := make([]bool, len(m.topology.leaves))
	for i, leaf := range m.topology.leaves {
		healthy[i] = m.healthy[leaf.LeafID]
	}
	return &availableCapacityPicker{
		leaves:        m.topology.leaves,
		healthyLeaves: m.healthyLeaves(),
		healthy:       healthy,
		table:         &m.pub,
	}
}

type availableCapacityPicker struct {
	leaves        []LeafTarget
	healthyLeaves []LeafTarget
	healthy       []bool
	table         *atomic.Pointer[capacityTable]
	counter       atomic.Uint64
}

func (p *availableCapacityPicker) Pick(req RouteRequest) (LeafTarget, error) {
	if len(p.healthyLeaves) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	outer := p.table.Load()
	if outer == nil {
		return p.coldFallback(req.FunctionID)
	}
	cell := (*outer)[req.FunctionID]
	if cell == nil {
		return p.coldFallback(req.FunctionID)
	}
	view := cell.pub.Load()
	if view == nil {
		return p.coldFallback(req.FunctionID)
	}
	var best uint64
	ties := 0
	for _, route := range view.entries {
		if !p.healthy[route.leaf] {
			continue
		}
		if ties == 0 || route.available > best {
			best = route.available
			ties = 1
		} else if route.available == best {
			ties++
		}
	}
	if ties == 0 {
		return p.coldFallback(req.FunctionID)
	}
	chosen := int((p.counter.Add(1) - 1) % uint64(ties))
	for _, route := range view.entries {
		if p.healthy[route.leaf] && route.available == best {
			if chosen == 0 {
				target := p.leaves[route.leaf]
				target.Reason = ReasonAvailableCapacity
				return target, nil
			}
			chosen--
		}
	}
	return p.coldFallback(req.FunctionID)
}

// coldFallback spreads a function with no healthy advertised route across
// healthy leaves by its function ID.
func (p *availableCapacityPicker) coldFallback(functionID uint64) (LeafTarget, error) {
	idx := hashFunctionID(functionID) % uint64(len(p.healthyLeaves))
	target := p.healthyLeaves[idx]
	target.Reason = ReasonColdCacheFallback
	return target, nil
}
