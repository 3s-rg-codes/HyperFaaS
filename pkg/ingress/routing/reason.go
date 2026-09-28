package routing

// Reason is the branch a picker took for one request. It is logged at debug.
type Reason uint8

const (
	ReasonRandom Reason = iota + 1
	ReasonRoundRobin
	ReasonConsistentHashingHome
	ReasonBoundedLoadsHome
	ReasonBoundedLoadsForwarded
	ReasonBoundedLoadsLeastLoaded
	ReasonRJCHHome
	ReasonRJCHJump
	ReasonRJCHLeastLoaded
	ReasonCHRLUHome
	ReasonCHRLUForwarded
	ReasonCHRLULeastLoaded
	ReasonLeastLoaded
	ReasonAvailableCapacity
	ReasonColdCacheFallback
)

func (r Reason) String() string {
	switch r {
	case ReasonRandom:
		return PolicyRandom
	case ReasonRoundRobin:
		return PolicyRoundRobin
	case ReasonConsistentHashingHome:
		return "consistent-hashing-home"
	case ReasonBoundedLoadsHome:
		return "bounded-loads-home"
	case ReasonBoundedLoadsForwarded:
		return "bounded-loads-forwarded"
	case ReasonBoundedLoadsLeastLoaded:
		return "bounded-loads-least-loaded"
	case ReasonRJCHHome:
		return "rj-ch-home"
	case ReasonRJCHJump:
		return "rj-ch-jump"
	case ReasonRJCHLeastLoaded:
		return "rj-ch-least-loaded"
	case ReasonCHRLUHome:
		return "ch-rlu-home"
	case ReasonCHRLUForwarded:
		return "ch-rlu-forwarded"
	case ReasonCHRLULeastLoaded:
		return "ch-rlu-least-loaded"
	case ReasonLeastLoaded:
		return PolicyLeastLoaded
	case ReasonAvailableCapacity:
		return PolicyAvailableCapacity
	case ReasonColdCacheFallback:
		return "cold-cache-fallback"
	default:
		return ""
	}
}
