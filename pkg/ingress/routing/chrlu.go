package routing

import (
	"math"
	"sync/atomic"
	"time"

	"hyperfaas-ideal-arch/pkg/chbl"
)

const chrluSampleSlots = 1 << 14

type chrluPolicy struct {
	bound, maxBound           float64
	coldTimeMS, warmTimeMS    float64
	popularIATMS, noiseStddev float64
	maxChainLen               int
	samplePercent             uint32
	latencies                 map[uint64]latencyEstimate
	heartbeatSeconds          float64
}

type latencyEstimate struct{ coldMS, warmMS float64 }

func (chrluPolicy) Needs() RoutingNeeds { return NeedLeafLoad }

func (p chrluPolicy) NewModel(t Topology) routingModel {
	base := boundedLoadsPolicy{bound: p.bound, maxChainLen: p.maxChainLen}.NewModel(t).(*boundedLoadsModel)
	maxBound := p.maxBound
	if maxBound <= 0 || math.IsNaN(maxBound) || math.IsInf(maxBound, 0) {
		maxBound = 6
	}
	samplePercent := p.samplePercent
	if samplePercent == 0 || samplePercent > 100 {
		samplePercent = 20
	}
	popularIAT := p.popularIATMS
	if popularIAT <= 0 || math.IsNaN(popularIAT) || math.IsInf(popularIAT, 0) {
		popularIAT = 1000
	}
	noiseStddev := p.noiseStddev
	if noiseStddev <= 0 || math.IsNaN(noiseStddev) || math.IsInf(noiseStddev, 0) {
		noiseStddev = 0.1
	}
	heartbeatSeconds := p.heartbeatSeconds
	if heartbeatSeconds <= 0 || math.IsNaN(heartbeatSeconds) || math.IsInf(heartbeatSeconds, 0) {
		heartbeatSeconds = 0.5
	}
	m := &chrluModel{
		boundedLoadsModel: base,
		samples:           make([]atomic.Pointer[iatSample], chrluSampleSlots),
		samplePercent:     samplePercent,
		popularIAT:        time.Duration(popularIAT * float64(time.Millisecond)),
		noiseStddev:       noiseStddev,
		maxBound:          maxBound,
		defaultLatency:    latencyEstimate{coldMS: p.coldTimeMS, warmMS: p.warmTimeMS},
		latencies:         p.latencies,
		heartbeatSeconds:  heartbeatSeconds,
	}
	m.counter.Store(uint64(time.Now().UnixNano()))
	return m
}

// IAT samples live in fixed atomic slots. A slot stores an immutable record;
// concurrent requests replace it with CAS, so Pick never reads a mutable map
// or slice. Hash collisions only cause a sample to start over.
type iatSample struct {
	functionID, lastUnixNano uint64
	averageIAT               time.Duration
}

type chrluModel struct {
	*boundedLoadsModel
	samples          []atomic.Pointer[iatSample]
	counter          atomic.Uint64
	samplePercent    uint32
	popularIAT       time.Duration
	noiseStddev      float64
	maxBound         float64
	defaultLatency   latencyEstimate
	latencies        map[uint64]latencyEstimate // copied from config, then immutable
	heartbeatSeconds float64
}

func (m *chrluModel) ReplaceLeaf(s LeafState) UpdateResult { return m.boundedLoadsModel.ReplaceLeaf(s) }
func (m *chrluModel) Apply(s LeafState) UpdateResult       { return m.boundedLoadsModel.Apply(s) }
func (m *chrluModel) LeafDisconnected(id uint64) UpdateResult {
	return m.boundedLoadsModel.LeafDisconnected(id)
}

func (m *chrluModel) Picker() Picker {
	return &chrluPicker{
		base:    m.boundedLoadsModel.Picker().(*boundedLoadsPicker),
		samples: m.samples, counter: &m.counter,
		samplePercent: m.samplePercent, popularIAT: m.popularIAT,
		noiseStddev: m.noiseStddev,
		maxBound:    m.maxBound, defaultLatency: m.defaultLatency, latencies: m.latencies,
		heartbeatSeconds: m.heartbeatSeconds,
	}
}

type chrluPicker struct {
	base             *boundedLoadsPicker
	samples          []atomic.Pointer[iatSample]
	counter          *atomic.Uint64
	samplePercent    uint32
	popularIAT       time.Duration
	noiseStddev      float64
	maxBound         float64
	defaultLatency   latencyEstimate
	latencies        map[uint64]latencyEstimate
	heartbeatSeconds float64
}

func (p *chrluPicker) boundFor(functionID uint64) float64 {
	estimate, ok := p.latencies[functionID]
	if !ok {
		estimate = p.defaultLatency
	}
	bound := p.base.bound
	if estimate.coldMS > 0 && estimate.warmMS > 0 {
		bound *= estimate.coldMS / estimate.warmMS
	}
	if math.IsNaN(bound) || math.IsInf(bound, 0) {
		bound = p.base.bound
	}
	return math.Min(bound, p.maxBound)
}

// observeIAT uses spatial sampling by function hash, as in SHARDS. The EWMA
// tracks sampled functions' inter-arrival time without growing per-function
// state with the workload. Unsampled and first-seen functions are not popular.
func (p *chrluPicker) observeIAT(functionID uint64, now time.Time) (time.Duration, bool) {
	h := hashFunctionID(functionID)
	if h%100 >= uint64(p.samplePercent) {
		return 0, false
	}
	slot := &p.samples[h%uint64(len(p.samples))]
	nowNS := uint64(now.UnixNano())
	for {
		old := slot.Load()
		if old != nil && old.functionID == functionID && nowNS <= old.lastUnixNano {
			return old.averageIAT, old.averageIAT > 0 && old.averageIAT <= p.popularIAT
		}
		next := &iatSample{functionID: functionID, lastUnixNano: nowNS}
		if old != nil && old.functionID == functionID {
			iat := time.Duration(nowNS - old.lastUnixNano)
			next.averageIAT = iat
			if old.averageIAT > 0 {
				next.averageIAT = (old.averageIAT + iat) / 2
			}
		}
		if slot.CompareAndSwap(old, next) {
			return next.averageIAT, next.averageIAT > 0 && next.averageIAT <= p.popularIAT
		}
	}
}

func (p *chrluPicker) Pick(req RouteRequest) (LeafTarget, error) {
	b := p.base
	if b.ring == nil || len(b.leaves) == 0 {
		return LeafTarget{}, noHealthyLeaves()
	}
	home := b.ring.Home(req.FunctionID)
	bound := p.boundFor(req.FunctionID)
	iat, popular := p.observeIAT(req.FunctionID, time.Now())
	if !popular {
		id, reason := chbl.ForwardBy(b.ring, b, bound, b.maxChainLen, home)
		target := b.byID[id]
		target.Reason = chrluReason(reason)
		return target, nil
	}

	// Expected extra arrivals use the local 500 ms state heartbeat. The paper
	// used a 5 s OpenWhisk update interval, so this mean is correspondingly
	// smaller here. Noise is only applied to the threshold test for popular
	// functions; the least-loaded fallback uses observed loads.
	mean := p.heartbeatSeconds / iat.Seconds()
	id := home
	for step := 0; step <= b.maxChainLen; step++ {
		load := b.Load(id) + mean + p.gaussian(req.FunctionID, id)*p.noiseStddev
		if load < bound {
			target := b.byID[id]
			if step == 0 {
				target.Reason = ReasonCHRLUHome
			} else {
				target.Reason = ReasonCHRLUForwarded
			}
			return target, nil
		}
		id = b.ring.Next(id)
	}
	id = b.LeastLoadedID()
	target := b.byID[id]
	target.Reason = ReasonCHRLULeastLoaded
	return target, nil
}

func chrluReason(reason string) Reason {
	switch reason {
	case chbl.ReasonHome:
		return ReasonCHRLUHome
	case chbl.ReasonForwarded:
		return ReasonCHRLUForwarded
	default:
		return ReasonCHRLULeastLoaded
	}
}

// Box-Muller from two independent splitmix-style hashes. The counter is
// shared across picker rebuilds and is the only per-Pick random state.
func (p *chrluPicker) gaussian(functionID, leafID uint64) float64 {
	x := p.counter.Add(1) ^ functionID ^ (leafID * 0x9e3779b97f4a7c15)
	u1 := float64((hashFunctionID(x)>>11)+1) / float64(uint64(1)<<53)
	u2 := float64(hashFunctionID(x^0xbf58476d1ce4e5b9)>>11) / float64(uint64(1)<<53)
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
