package perf

import (
	"testing"

	"hyperfaas-ideal-arch/test/shared"
)

func TestPerfSingleUserSingleFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf tests in -short mode")
	}

	h := shared.NewHarness(t)
	cfg := shared.LoadPerfConfig()

	t.Run("concurrency_unlimited", func(t *testing.T) {
		result := runPerfScenario(t, h, cfg, perfScenario{
			numUsers:             1,
			containerConcurrency: 0,
			maxInstances:         5,
			clientConcurrency:    cfg.HighLoadConcurrency,
		})
		if result.AvgRPSPerFunction < 1200 {
			t.Errorf("expected high RPS for unlimited concurrency (>= 1200), got %.2f", result.AvgRPSPerFunction)
		}
	})

	t.Run("concurrency_1", func(t *testing.T) {
		result := runPerfScenario(t, h, cfg, perfScenario{
			numUsers:             1,
			containerConcurrency: 1,
			// Pin to one instance so scale-out does not mask per-container concurrency.
			maxInstances:      1,
			clientConcurrency: cfg.HighLoadConcurrency,
		})
		// With cc=1 and one container, many concurrent clients queue behind a single slot.
		// echo-http on localhost is ~0.5ms/req, so absolute RPS stays in low hundreds, not ~15.
		if result.AvgRPSPerFunction > 500 {
			t.Errorf("expected throttled RPS for concurrency=1 on one instance (<= 500), got %.2f", result.AvgRPSPerFunction)
		}
		if result.AvgRPSPerFunction < 30 {
			t.Errorf("expected some throughput for concurrency=1 (>= 30), got %.2f", result.AvgRPSPerFunction)
		}
	})
}

func TestPerfMultiUserSingleFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf tests in -short mode")
	}

	h := shared.NewHarness(t)
	cfg := shared.LoadPerfConfig()

	t.Run("concurrency_unlimited", func(t *testing.T) {
		result := runPerfScenario(t, h, cfg, perfScenario{
			numUsers:             3,
			containerConcurrency: 0,
			maxInstances:         5,
			clientConcurrency:    cfg.HighLoadConcurrency,
		})
		if result.AvgRPSPerFunction < 400 {
			t.Errorf("expected high RPS per function for unlimited concurrency (>= 400), got %.2f", result.AvgRPSPerFunction)
		}
	})

	t.Run("concurrency_1", func(t *testing.T) {
		result := runPerfScenario(t, h, cfg, perfScenario{
			numUsers:             3,
			containerConcurrency: 1,
			maxInstances:         1,
			clientConcurrency:    cfg.HighLoadConcurrency,
		})
		if result.AvgRPSPerFunction > 200 {
			t.Errorf("expected throttled RPS per function for concurrency=1 (<= 200), got %.2f", result.AvgRPSPerFunction)
		}
		if result.AvgRPSPerFunction < 10 {
			t.Errorf("expected some throughput per function for concurrency=1 (>= 10), got %.2f", result.AvgRPSPerFunction)
		}
	})
}
