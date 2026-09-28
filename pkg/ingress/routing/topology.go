package routing

import "sort"

// Topology is the configured leaf membership.
//
// It is static for the lifetime of an ingress process and comes from YAML. Leaf
// health is layered on top from routing-state frames; it is not part of the
// topology.
type Topology struct {
	leaves  []LeafTarget
	index   map[uint64]int
	control map[uint64]string
}

// NewTopology builds a topology from configured leaf addresses.
func NewTopology(addrs map[uint64]LeafAddress) Topology {
	ids := make([]uint64, 0, len(addrs))
	for id := range addrs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	t := Topology{
		leaves:  make([]LeafTarget, 0, len(ids)),
		index:   make(map[uint64]int, len(ids)),
		control: make(map[uint64]string, len(ids)),
	}
	for _, id := range ids {
		t.index[id] = len(t.leaves)
		t.leaves = append(t.leaves, LeafTarget{LeafID: id, HTTPAddress: addrs[id].HTTPAddress})
		t.control[id] = addrs[id].ControlAddress
	}
	return t
}

// Leaves returns the configured leaves in ascending leaf-ID order.
func (t Topology) Leaves() []LeafTarget { return t.leaves }

// IDs returns the configured leaf IDs in ascending order.
func (t Topology) IDs() []uint64 {
	ids := make([]uint64, len(t.leaves))
	for i, l := range t.leaves {
		ids[i] = l.LeafID
	}
	return ids
}

// ControlAddress returns the leaf's routing-state stream address, or "".
func (t Topology) ControlAddress(leafID uint64) string { return t.control[leafID] }

// membership tracks which configured leaves are currently routable. A leaf is
// routable when it reports at least one healthy worker; stream liveness alone
// is not enough because a leaf can stay connected with no usable workers.
type membership struct {
	topology Topology
	healthy  map[uint64]bool
}

func newMembership(topology Topology) membership {
	return membership{topology: topology, healthy: make(map[uint64]bool, len(topology.leaves))}
}

// setHealthy records a leaf's health. It returns true when the value changed.
func (m *membership) setHealthy(leafID uint64, healthy bool) bool {
	changed := m.healthy[leafID] != healthy
	m.healthy[leafID] = healthy
	return changed
}

// healthyLeaves returns the routable leaves in topology order.
func (m *membership) healthyLeaves() []LeafTarget {
	out := make([]LeafTarget, 0, len(m.topology.leaves))
	for _, l := range m.topology.leaves {
		if m.healthy[l.LeafID] {
			out = append(out, l)
		}
	}
	return out
}
