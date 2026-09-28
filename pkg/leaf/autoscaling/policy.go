package autoscaling

import (
	"context"
	"math"
	"sync"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

const (
	defaultScalingPeriod       = 2 * time.Second
	defaultStableWindow        = 6 * time.Second
	defaultPanicWindow         = 2 * time.Second
	defaultPanicThresholdRatio = 2.0
	defaultMaxScaleUpRate      = 1000.0
	defaultMaxScaleDownRate    = 2.0
)

// DefaultPolicy implements Dirigent-style desired scale from in-flight
// concurrency: 2s samples, a 6s stable window, a 2s panic window, panic-mode
// scale-down suppression, and scale-down limited to half the current scale per
// decision. It still honors HyperFaaS' per-function scale-to-zero idle timeout.
type DefaultPolicy struct {
	ScaleToZeroAfter           time.Duration
	UnlimitedConcurrencyTarget uint64
	// TargetUtilization is Knative's container-concurrency-target-percentage.
	// 0 defaults to 1.0 (no headroom). Values in (1, 100] are percentages.
	TargetUtilization     float64
	ColdStartPanicScaling bool
	DirigentAdmission     bool
	StableWindow          time.Duration
	PanicWindow           time.Duration
	PanicThresholdRatio   float64
	MaxScaleUpRate        float64
	MaxScaleDownRate      float64

	mu     sync.Mutex
	states map[uint64]*dirigentState
}

type dirigentState struct {
	buckets           []float64
	windowHead        int
	lastSample        time.Time
	inPanic           bool
	panicStarted      time.Time
	maxPanicInstances uint64
}

func (p *DefaultPolicy) DesiredScale(_ context.Context, function *core.FunctionSpec, signals Signals, now time.Time) (*core.ScaleDecision, error) {
	var maxCC int
	if function.GetScale() != nil {
		maxCC = int(function.GetScale().GetMaxConcurrency())
	} else {
		maxCC = 1
	}

	current := uint64(signals.ReadyInstances)
	if p.DirigentAdmission && signals.HasLogicalScale {
		current = signals.LogicalScale
	} else if p.DirigentAdmission {
		current += uint64(signals.PendingStarts)
	}
	var desired uint64
	var reason string
	var stable bool
	observedDemand := signals.InFlight
	if signals.HighWater > observedDemand {
		observedDemand = signals.HighWater
	}

	switch {
	case observedDemand > 0:
		if p.DirigentAdmission && current == 0 {
			desired = 1
			reason = "dirigent-scale-from-zero"
		} else if p.DirigentAdmission {
			desired = p.dirigentDesired(function, current, observedDemand, now)
			reason = "dirigent-optimistic"
		} else if p.ColdStartPanicScaling && maxCC == 1 {
			desired = observedDemand
			if desired < current {
				desired = current
			}
			reason = "cold-start-panic"
		} else {
			desired = p.dirigentDesired(function, current, observedDemand, now)
			reason = "dirigent-in-flight"
		}
	case current == 0:
		desired = 0
		reason = "zero"
		stable = true
	default:
		idle := p.ScaleToZeroAfter
		if fnIdle := function.GetScale().GetScaleToZeroIdleTimeout(); fnIdle != nil && fnIdle.AsDuration() > 0 {
			idle = fnIdle.AsDuration()
		}
		if !signals.LastActivity.IsZero() && now.Sub(signals.LastActivity) >= idle {
			desired = 0
			reason = "scale-to-zero"
		} else {
			desired = p.dirigentDesired(function, current, 0, now)
			if desired == 0 && current > 0 {
				desired = 1
			}
			reason = "dirigent-idle"
		}
	}

	desired = clampDesired(function, desired)
	return &core.ScaleDecision{
		FunctionId:       function.GetFunctionId(),
		DesiredInstances: desired,
		Reason:           reason,
		Stable:           stable,
	}, nil
}

func (p *DefaultPolicy) dirigentDesired(function *core.FunctionSpec, current, inFlight uint64, now time.Time) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	var functionID uint64
	if function != nil {
		functionID = function.GetFunctionId()
	}
	if p.states == nil {
		p.states = make(map[uint64]*dirigentState)
	}
	state := p.states[functionID]
	if state == nil {
		state = &dirigentState{}
		p.states[functionID] = state
	}
	stableWindow := p.stableWindow()
	panicWindow := p.panicWindow()
	state.record(float64(inFlight), now, stableWindow)

	target := resolveScaleTarget(function, p.UnlimitedConcurrencyTarget, p.TargetUtilization)
	stableAvg := state.average(stableWindow)
	panicAvg := state.average(panicWindow)

	ready := current
	if ready == 0 {
		ready = 1
	}
	maxScaleUp := uint64(math.Ceil(p.maxScaleUpRate() * float64(ready)))
	maxScaleDown := uint64(math.Floor(float64(ready) / p.maxScaleDownRate()))

	desiredStable := clampBetween(ceilDivFloat(stableAvg, target), maxScaleDown, maxScaleUp)
	desiredPanic := clampBetween(ceilDivFloat(panicAvg, target), maxScaleDown, maxScaleUp)
	// A moving average is useful for deciding when to remove capacity, but it
	// must not hide work that is queued right now. Without this floor, a sharp
	// increase below the panic threshold can leave the fleet undersized for a
	// full stable window.
	desiredInstant := clampBetween(ceilDivFloat(float64(inFlight), target), 0, maxScaleUp)
	desired := desiredStable

	if float64(desiredPanic)/float64(ready) >= p.panicThresholdRatio() {
		if !state.inPanic {
			state.inPanic = true
			state.panicStarted = now
		} else {
			// Stay in panic mode for a full stable window after the latest
			// high sample.
			state.panicStarted = now
		}
		if desiredPanic > state.maxPanicInstances {
			state.maxPanicInstances = desiredPanic
		}
	} else if state.inPanic && state.panicStarted.Add(stableWindow).Before(now) {
		state.inPanic = false
		state.panicStarted = time.Time{}
		state.maxPanicInstances = 0
	}

	if state.inPanic {
		if desired < desiredPanic {
			desired = desiredPanic
		}
		if desired < state.maxPanicInstances {
			desired = state.maxPanicInstances
		}
	}
	if desired < desiredInstant {
		desired = desiredInstant
	}

	return desired
}

func (p *DefaultPolicy) stableWindow() time.Duration {
	if p.StableWindow > 0 {
		return p.StableWindow
	}
	return defaultStableWindow
}

func (p *DefaultPolicy) panicWindow() time.Duration {
	if p.PanicWindow > 0 {
		return p.PanicWindow
	}
	return defaultPanicWindow
}

func (p *DefaultPolicy) panicThresholdRatio() float64 {
	if p.PanicThresholdRatio > 0 {
		return p.PanicThresholdRatio
	}
	return defaultPanicThresholdRatio
}

func (p *DefaultPolicy) maxScaleUpRate() float64 {
	if p.MaxScaleUpRate > 0 {
		return p.MaxScaleUpRate
	}
	return defaultMaxScaleUpRate
}

func (p *DefaultPolicy) maxScaleDownRate() float64 {
	if p.MaxScaleDownRate > 0 {
		return p.MaxScaleDownRate
	}
	return defaultMaxScaleDownRate
}

func (s *dirigentState) record(value float64, now time.Time, stableWindow time.Duration) {
	if s.lastSample.IsZero() || now.Sub(s.lastSample) >= defaultScalingPeriod || len(s.buckets) == 0 {
		maxBuckets := int(stableWindow / defaultScalingPeriod)
		if maxBuckets < 1 {
			maxBuckets = 1
		}
		if len(s.buckets) < maxBuckets {
			s.buckets = append(s.buckets, value)
			s.windowHead = len(s.buckets) - 1
		} else {
			s.windowHead = (s.windowHead + 1) % maxBuckets
			s.buckets[s.windowHead] = value
		}
		s.lastSample = now
		return
	}
	s.buckets[s.windowHead] = value
}

func (s *dirigentState) average(window time.Duration) float64 {
	if len(s.buckets) == 0 {
		return 0
	}
	count := int(window / defaultScalingPeriod)
	if count < 1 {
		count = 1
	}
	if count > len(s.buckets) {
		count = len(s.buckets)
	}
	sum := 0.0
	idx := s.windowHead
	for i := 0; i < count; i++ {
		sum += s.buckets[idx]
		idx--
		if idx < 0 {
			idx = len(s.buckets) - 1
		}
	}
	return sum / float64(count)
}

const minScaleTarget = 0.01

// resolveScaleTarget is Knative ResolveMetricTarget without annotations:
// max_concurrency is the hard breaker (0 = unlimited); target_concurrency is
// the optional soft autoscaler setpoint; otherwise the leaf default applies.
func resolveScaleTarget(function *core.FunctionSpec, defaultTotal uint64, utilization float64) float64 {
	var maxCC, annotated uint64
	if function == nil || function.GetScale() == nil {
		maxCC = 1
	} else {
		maxCC = function.GetScale().GetMaxConcurrency()
		annotated = function.GetScale().GetTargetConcurrency()
	}

	total := float64(maxCC)
	if total == 0 {
		if annotated > 0 {
			total = float64(annotated)
		} else if defaultTotal > 0 {
			total = float64(defaultTotal)
		} else {
			total = 1
		}
	} else if annotated > 0 {
		total = math.Min(float64(annotated), total)
	}

	return math.Max(minScaleTarget, total*normalizeUtilization(utilization))
}

func normalizeUtilization(v float64) float64 {
	if v <= 0 {
		return 1
	}
	if v > 1 {
		v /= 100
	}
	if v <= 0 || v > 1 {
		return 1
	}
	return v
}

func ceilDivFloat(value, divisor float64) uint64 {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return uint64(math.Ceil(value / divisor))
}

func clampBetween(value, min, max uint64) uint64 {
	if value < min {
		value = min
	}
	if max > 0 && value > max {
		value = max
	}
	return value
}

func clampDesired(function *core.FunctionSpec, desired uint64) uint64 {
	if function.GetScale() == nil {
		return desired
	}
	min := function.GetScale().GetMinInstances()
	max := function.GetScale().GetMaxInstances()
	if desired < min {
		desired = min
	}
	if max > 0 && desired > max {
		desired = max
	}
	return desired
}
