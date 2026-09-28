// Package chbl implements Fuerst CH-BL (Consistent Hashing with Bounded Loads).
//
// Paper: Fuerst and Sharma, "Locality-aware Load-Balancing For Serverless Clusters",
// HPDC 2022, Section 4.2 / Algorithm 2. This round uses the paper's absolute bound b
// on normalized 1-minute load average. It does not use Mirrokni (1+ε)×average,
// SHARDS popularity, Gaussian stale-load noise, or min(cb/w, b_max).
package chbl

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
)

const (
	// DefaultBound is a fully busy machine (loadavg / ncpus == 1).
	DefaultBound = 1.0
	// DefaultMaxChainLen is the paper's forwarding cap.
	DefaultMaxChainLen = 3
	// ringReplicas is the number of virtual positions per node. The paper does not
	// define this; virtual nodes keep the ring even.
	ringReplicas = 128

	ReasonHome        = "bounded-loads-home"
	ReasonForwarded   = "bounded-loads-forwarded"
	ReasonLeastLoaded = "bounded-loads-least-loaded"
)

// NormalizeBound returns b, or DefaultBound when b is unset.
func NormalizeBound(bound float64) float64 {
	if bound <= 0 {
		return DefaultBound
	}
	return bound
}

// NormalizeMaxChainLen returns max_chain_len, or DefaultMaxChainLen when unset.
func NormalizeMaxChainLen(maxChainLen int) int {
	if maxChainLen <= 0 {
		return DefaultMaxChainLen
	}
	return maxChainLen
}

type ringNode struct {
	position uint64
	id       uint64
}

// Ring is a consistent-hashing ring over node IDs (leaves or workers).
type Ring struct {
	nodes []ringNode
	ids   []uint64
	// first is the index of each ID's first (lowest-position) node. Next starts
	// here, so it does not rescan the whole ring to locate the ID.
	first map[uint64]int
}

// NewRing places each ID on the ring with virtual nodes.
func NewRing(ids []uint64) *Ring {
	uniq := append([]uint64(nil), ids...)
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })
	nodes := make([]ringNode, 0, len(uniq)*ringReplicas)
	for _, id := range uniq {
		for i := 0; i < ringReplicas; i++ {
			key := fmt.Sprintf("%d:%d", id, i)
			nodes = append(nodes, ringNode{position: hashString(key), id: id})
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].position == nodes[j].position {
			return nodes[i].id < nodes[j].id
		}
		return nodes[i].position < nodes[j].position
	})
	first := make(map[uint64]int, len(uniq))
	for i, node := range nodes {
		if _, ok := first[node.id]; !ok {
			first[node.id] = i
		}
	}
	return &Ring{nodes: nodes, ids: uniq, first: first}
}

// Subset returns a ring containing only healthy IDs. It keeps the positions
// computed by NewRing, so its result is the same as rebuilding from those IDs.
// The original ring remains available for later health changes.
func (h *Ring) Subset(healthy map[uint64]bool) *Ring {
	count := 0
	for _, id := range h.ids {
		if healthy[id] {
			count++
		}
	}
	if count == len(h.ids) {
		return h
	}
	ids := make([]uint64, 0, count)
	for _, id := range h.ids {
		if healthy[id] {
			ids = append(ids, id)
		}
	}
	nodes := make([]ringNode, 0, len(h.nodes)*count/len(h.ids))
	first := make(map[uint64]int, count)
	for _, node := range h.nodes {
		if !healthy[node.id] {
			continue
		}
		if _, ok := first[node.id]; !ok {
			first[node.id] = len(nodes)
		}
		nodes = append(nodes, node)
	}
	return &Ring{nodes: nodes, ids: ids, first: first}
}

// IDs returns the ring members in ascending order.
func (h *Ring) IDs() []uint64 {
	return h.ids
}

// Home returns the first node clockwise from the function's ring position.
func (h *Ring) Home(functionID uint64) uint64 {
	if len(h.nodes) == 0 {
		return 0
	}
	position := FunctionPosition(functionID)
	idx := sort.Search(len(h.nodes), func(i int) bool { return h.nodes[i].position >= position })
	if idx == len(h.nodes) {
		idx = 0
	}
	return h.nodes[idx].id
}

// Next returns the next distinct node clockwise from id.
//
// It starts at the ID's first ring position, which is precomputed in NewRing.
// The remaining walk skips only the ID's own replicas that happen to be
// adjacent, so it is O(1) on average rather than O(len(nodes)).
func (h *Ring) Next(id uint64) uint64 {
	if len(h.nodes) == 0 {
		return id
	}
	idx, ok := h.first[id]
	if !ok {
		return id
	}
	for i := 1; i <= len(h.nodes); i++ {
		node := h.nodes[(idx+i)%len(h.nodes)]
		if node.id != id {
			return node.id
		}
	}
	return id
}

// LoadReader lets a caller keep its load values outside a map, such as in
// atomic slots read by a routing picker.
type LoadReader interface {
	Load(id uint64) float64
}

// LeastLoadedScanner is an optional LoadReader extension. A reader that owns a
// slot layout aligned with the ring IDs can scan it directly instead of calling
// Load once per ID. It must return the same ID as LeastLoadedBy(ring.IDs(), r).
type LeastLoadedScanner interface {
	LeastLoadedID() uint64
}

// Forward keeps the map path used by worker placement allocation-free. Its
// decision rules match ForwardBy, which reads a policy-owned load layout.
func Forward(ring *Ring, loads map[uint64]float64, bound float64, maxChainLen int, home uint64) (uint64, string) {
	bound = NormalizeBound(bound)
	maxChainLen = NormalizeMaxChainLen(maxChainLen)
	if ring == nil || len(ring.ids) == 0 {
		return home, ReasonHome
	}
	id := home
	for step := 0; step <= maxChainLen; step++ {
		if loads[id] < bound {
			if step == 0 {
				return id, ReasonHome
			}
			return id, ReasonForwarded
		}
		id = ring.Next(id)
	}
	return LeastLoaded(ring.ids, loads), ReasonLeastLoaded
}

// ForwardBy reads loads through the caller's storage layout. It stays at home
// below the bound, walks at most maxChainLen successors, then scans all IDs.
func ForwardBy(ring *Ring, loads LoadReader, bound float64, maxChainLen int, home uint64) (uint64, string) {
	bound = NormalizeBound(bound)
	maxChainLen = NormalizeMaxChainLen(maxChainLen)
	if ring == nil || len(ring.ids) == 0 {
		return home, ReasonHome
	}
	id := home
	for step := 0; step <= maxChainLen; step++ {
		if loads.Load(id) < bound {
			if step == 0 {
				return id, ReasonHome
			}
			return id, ReasonForwarded
		}
		id = ring.Next(id)
	}
	if scanner, ok := loads.(LeastLoadedScanner); ok {
		return scanner.LeastLoadedID(), ReasonLeastLoaded
	}
	return LeastLoadedBy(ring.ids, loads), ReasonLeastLoaded
}

// LeastLoaded returns the ID with the lowest load; ties break to the lowest ID.
func LeastLoaded(ids []uint64, loads map[uint64]float64) uint64 {
	if len(ids) == 0 {
		return 0
	}
	best := ids[0]
	bestLoad := loads[best]
	for _, id := range ids[1:] {
		load := loads[id]
		if load < bestLoad || (load == bestLoad && id < best) {
			best = id
			bestLoad = load
		}
	}
	return best
}

// LeastLoadedBy reads loads through the caller's storage layout.
func LeastLoadedBy(ids []uint64, loads LoadReader) uint64 {
	if len(ids) == 0 {
		return 0
	}
	best := ids[0]
	bestLoad := loads.Load(best)
	for _, id := range ids[1:] {
		load := loads.Load(id)
		if load < bestLoad || (load == bestLoad && id < best) {
			best = id
			bestLoad = load
		}
	}
	return best
}

// FunctionPosition returns the ring position of the function.
func FunctionPosition(functionID uint64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], functionID)
	h := fnv.New64a()
	_, _ = h.Write(buf[:])
	return mixHash(h.Sum64())
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return mixHash(h.Sum64())
}

func mixHash(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
