package autoscaling

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"hyperfaas-ideal-arch/pkg/core"
)

func TestDefaultPolicyScaleToZeroAfterIdle(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	now := time.Now()

	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 1,
		LastActivity:   now.Add(-2 * time.Second),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 0 || decision.GetReason() != "scale-to-zero" {
		t.Fatalf("got desired=%d reason=%q", decision.GetDesiredInstances(), decision.GetReason())
	}
}

func TestDefaultPolicyRateLimitsDownscaleBeforeIdleTimeout(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Minute}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	now := time.Now()

	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 2,
		LastActivity:   now.Add(-10 * time.Second),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 1 || decision.GetReason() != "dirigent-idle" {
		t.Fatalf("got desired=%d reason=%q", decision.GetDesiredInstances(), decision.GetReason())
	}
}

func TestDefaultPolicyRespectsMaxInstances(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale: &core.ScalePolicySpec{
			MaxConcurrency: 1,
			MaxInstances:   1,
		},
	}
	decision, err := p.DesiredScale(context.Background(), fn, Signals{InFlight: 5}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 1 {
		t.Fatalf("got desired=%d want 1", decision.GetDesiredInstances())
	}
}

func TestDefaultPolicyUsesRetainedHighWaterForScaleUp(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Minute}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 1,
		InFlight:       0,
		HighWater:      8,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.GetDesiredInstances(); got != 8 {
		t.Fatalf("desired instances = %d, want retained burst demand 8", got)
	}
}

func TestDefaultPolicyPerFunctionIdleTimeout(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Minute}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale: &core.ScalePolicySpec{
			MaxConcurrency:         1,
			ScaleToZeroIdleTimeout: durationpb.New(100 * time.Millisecond),
		},
	}
	now := time.Now()
	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 1,
		LastActivity:   now.Add(-200 * time.Millisecond),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 0 {
		t.Fatalf("got desired=%d want 0", decision.GetDesiredInstances())
	}
}

func TestDefaultPolicyUnlimitedConcurrency(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second, UnlimitedConcurrencyTarget: 50}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale: &core.ScalePolicySpec{
			MaxConcurrency: 0, // unlimited breaker; leaf default is the soft target
			MaxInstances:   5,
		},
	}
	decision, err := p.DesiredScale(context.Background(), fn, Signals{InFlight: 100}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 2 {
		t.Fatalf("got desired=%d want 2", decision.GetDesiredInstances())
	}
}

func TestDefaultPolicyKnativeSoftTarget(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		maxCC        uint64
		targetCC     uint64
		defaultTotal uint64
		utilization  float64
		inFlight     uint64
		want         uint64
	}{
		{name: "unlimited default 100 holds at 100 in-flight", maxCC: 0, defaultTotal: 100, utilization: 1, inFlight: 100, want: 1},
		{name: "unlimited default 100 scales at 101", maxCC: 0, defaultTotal: 100, utilization: 1, inFlight: 101, want: 2},
		{name: "per-function target beats leaf default", maxCC: 0, targetCC: 10, defaultTotal: 100, utilization: 1, inFlight: 100, want: 10},
		{name: "max_concurrency 1 still divides by 1", maxCC: 1, defaultTotal: 100, utilization: 1, inFlight: 5, want: 5},
		{name: "target cannot exceed max_concurrency", maxCC: 10, targetCC: 20, defaultTotal: 100, utilization: 1, inFlight: 10, want: 1},
		{name: "target below max_concurrency", maxCC: 10, targetCC: 1, defaultTotal: 100, utilization: 1, inFlight: 10, want: 10},
		{name: "70 percent utilization holds at 70", maxCC: 0, defaultTotal: 100, utilization: 0.7, inFlight: 70, want: 1},
		{name: "70 percent utilization scales at 71", maxCC: 0, defaultTotal: 100, utilization: 0.7, inFlight: 71, want: 2},
		{name: "utilization 70 means 70 percent", maxCC: 0, defaultTotal: 100, utilization: 70, inFlight: 70, want: 1},
		{name: "zero utilization defaults to 1.0", maxCC: 0, defaultTotal: 100, utilization: 0, inFlight: 100, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &DefaultPolicy{
				ScaleToZeroAfter:           time.Second,
				UnlimitedConcurrencyTarget: tc.defaultTotal,
				TargetUtilization:          tc.utilization,
			}
			fn := &core.FunctionSpec{
				FunctionId: 1,
				Scale: &core.ScalePolicySpec{
					MaxConcurrency:    tc.maxCC,
					TargetConcurrency: tc.targetCC,
					MaxInstances:      1000,
				},
			}
			decision, err := p.DesiredScale(context.Background(), fn, Signals{InFlight: tc.inFlight}, now)
			if err != nil {
				t.Fatal(err)
			}
			if decision.GetDesiredInstances() != tc.want {
				t.Fatalf("got desired=%d want %d", decision.GetDesiredInstances(), tc.want)
			}
		})
	}
}

func TestResolveScaleTargetKnativeCases(t *testing.T) {
	fn := func(maxCC, targetCC uint64) *core.FunctionSpec {
		return &core.FunctionSpec{Scale: &core.ScalePolicySpec{MaxConcurrency: maxCC, TargetConcurrency: targetCC}}
	}
	tests := []struct {
		name         string
		function     *core.FunctionSpec
		defaultTotal uint64
		utilization  float64
		want         float64
	}{
		{name: "nil spec uses max 1", function: nil, defaultTotal: 100, utilization: 1, want: 1},
		{name: "unlimited uses leaf default", function: fn(0, 0), defaultTotal: 100, utilization: 1, want: 100},
		{name: "unlimited uses annotated target", function: fn(0, 10), defaultTotal: 100, utilization: 1, want: 10},
		{name: "cc times utilization", function: fn(10, 0), defaultTotal: 100, utilization: 0.8, want: 8},
		{name: "annotation capped by cc", function: fn(10, 1), defaultTotal: 100, utilization: 1, want: 1},
		{name: "percentage utilization", function: fn(0, 0), defaultTotal: 100, utilization: 70, want: 70},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveScaleTarget(tc.function, tc.defaultTotal, tc.utilization)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestDefaultPolicyDirigentPanicModeSuppressesDownscale(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	now := time.Now()

	samples := []struct {
		ready    uint32
		inFlight uint64
	}{
		{ready: 1, inFlight: 30},
		{ready: 30, inFlight: 30},
		{ready: 30, inFlight: 0},
	}
	for i, sample := range samples {
		decision, err := p.DesiredScale(context.Background(), fn, Signals{
			ReadyInstances: sample.ready,
			InFlight:       sample.inFlight,
		}, now.Add(time.Duration(i)*defaultScalingPeriod))
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 && decision.GetDesiredInstances() != 30 {
			t.Fatalf("got desired=%d want panic-mode hold at 30", decision.GetDesiredInstances())
		}
	}
}

func TestDefaultPolicyDirigentLimitsScaleDownRate(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale:      &core.ScalePolicySpec{MaxConcurrency: 1},
	}
	now := time.Now()

	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 100,
		InFlight:       1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 50 {
		t.Fatalf("got desired=%d want max-scale-down floor 50", decision.GetDesiredInstances())
	}
}

func TestDefaultPolicyInstantDemandFloorsStableAverage(t *testing.T) {
	p := &DefaultPolicy{
		ScaleToZeroAfter:           time.Minute,
		UnlimitedConcurrencyTarget: 100,
		StableWindow:               10 * time.Second,
	}
	fn := &core.FunctionSpec{
		FunctionId: 1,
		Scale: &core.ScalePolicySpec{
			MaxConcurrency: 100,
			MaxInstances:   1000,
		},
	}
	now := time.Now()

	if _, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 10,
		InFlight:       100,
	}, now); err != nil {
		t.Fatal(err)
	}
	decision, err := p.DesiredScale(context.Background(), fn, Signals{
		ReadyInstances: 10,
		InFlight:       900,
	}, now.Add(defaultScalingPeriod))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.GetDesiredInstances(); got != 9 {
		t.Fatalf("desired instances = %d, want instantaneous floor 9", got)
	}
}

func TestDefaultPolicyUsesConfiguredAutoscalingKnobs(t *testing.T) {
	p := &DefaultPolicy{
		StableWindow:               60 * time.Second,
		PanicWindow:                6 * time.Second,
		PanicThresholdRatio:        3,
		MaxScaleUpRate:             4,
		MaxScaleDownRate:           4,
		UnlimitedConcurrencyTarget: 100,
	}
	if got := p.stableWindow(); got != 60*time.Second {
		t.Fatalf("stable window=%s", got)
	}
	if got := p.panicWindow(); got != 6*time.Second {
		t.Fatalf("panic window=%s", got)
	}
	if got := p.panicThresholdRatio(); got != 3 {
		t.Fatalf("panic threshold ratio=%v", got)
	}
	if got := p.maxScaleUpRate(); got != 4 {
		t.Fatalf("max scale-up rate=%v", got)
	}
	if got := p.maxScaleDownRate(); got != 4 {
		t.Fatalf("max scale-down rate=%v", got)
	}
}

func TestDefaultPolicyZeroAutoscalingKnobsPreserveDefaults(t *testing.T) {
	p := &DefaultPolicy{}
	if got := p.stableWindow(); got != defaultStableWindow {
		t.Fatalf("stable window=%s want %s", got, defaultStableWindow)
	}
	if got := p.panicWindow(); got != defaultPanicWindow {
		t.Fatalf("panic window=%s want %s", got, defaultPanicWindow)
	}
	if got := p.panicThresholdRatio(); got != defaultPanicThresholdRatio {
		t.Fatalf("panic threshold ratio=%v want %v", got, defaultPanicThresholdRatio)
	}
	if got := p.maxScaleUpRate(); got != defaultMaxScaleUpRate {
		t.Fatalf("max scale-up rate=%v want %v", got, defaultMaxScaleUpRate)
	}
	if got := p.maxScaleDownRate(); got != defaultMaxScaleDownRate {
		t.Fatalf("max scale-down rate=%v want %v", got, defaultMaxScaleDownRate)
	}
}

func TestDefaultPolicyDirigentTracksStatePerFunction(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second}
	fn1 := &core.FunctionSpec{FunctionId: 1, Scale: &core.ScalePolicySpec{MaxConcurrency: 1}}
	fn2 := &core.FunctionSpec{FunctionId: 2, Scale: &core.ScalePolicySpec{MaxConcurrency: 1}}
	now := time.Now()

	if _, err := p.DesiredScale(context.Background(), fn1, Signals{ReadyInstances: 10, InFlight: 30}, now); err != nil {
		t.Fatal(err)
	}
	decision, err := p.DesiredScale(context.Background(), fn2, Signals{ReadyInstances: 10, InFlight: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 5 {
		t.Fatalf("got desired=%d want independent function state", decision.GetDesiredInstances())
	}
}

func TestDirigentAdmissionScaleFromZeroStartsOneReplica(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second, DirigentAdmission: true}
	fn := &core.FunctionSpec{FunctionId: 1, Scale: &core.ScalePolicySpec{MaxConcurrency: 1}}

	decision, err := p.DesiredScale(context.Background(), fn, Signals{InFlight: 150}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 1 || decision.GetReason() != "dirigent-scale-from-zero" {
		t.Fatalf("got desired=%d reason=%q, want 1 dirigent-scale-from-zero", decision.GetDesiredInstances(), decision.GetReason())
	}
}

func TestDirigentAdmissionCountsPendingStartsAsActualScale(t *testing.T) {
	p := &DefaultPolicy{ScaleToZeroAfter: time.Second, DirigentAdmission: true}
	fn := &core.FunctionSpec{FunctionId: 1, Scale: &core.ScalePolicySpec{MaxConcurrency: 1}}
	now := time.Now()

	decision, err := p.DesiredScale(context.Background(), fn, Signals{PendingStarts: 1, InFlight: 150}, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 150 || decision.GetReason() != "dirigent-optimistic" {
		t.Fatalf("got desired=%d reason=%q, want 150 dirigent-optimistic", decision.GetDesiredInstances(), decision.GetReason())
	}

	decision, err = p.DesiredScale(context.Background(), fn, Signals{PendingStarts: 150, InFlight: 150}, now.Add(defaultScalingPeriod))
	if err != nil {
		t.Fatal(err)
	}
	if decision.GetDesiredInstances() != 150 {
		t.Fatalf("got desired=%d, want pending starts to suppress duplicate scale-out at 150", decision.GetDesiredInstances())
	}
}

func BenchmarkResolveScaleTarget(b *testing.B) {
	fn := &core.FunctionSpec{Scale: &core.ScalePolicySpec{MaxConcurrency: 0, TargetConcurrency: 10}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if resolveScaleTarget(fn, 100, 1) != 10 {
			b.Fatal("unexpected target")
		}
	}
}
