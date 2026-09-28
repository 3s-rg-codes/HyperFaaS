package perf

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

type perfFunction struct {
	userID     uint64
	functionID uint64
}

// PerfResult captures throughput stats from a benchmark run.
type PerfResult struct {
	Users              int
	ContainerConcurrency uint64
	Duration           time.Duration
	SuccessInvokes     uint64
	FailureInvokes     uint64
	TotalRPS           float64
	AvgRPSPerFunction  float64
	FailureRate        float64
}

type perfScenario struct {
	numUsers             int
	containerConcurrency uint64
	maxInstances         uint64
	clientConcurrency    int
}

func runPerfScenario(t *testing.T, h *shared.Harness, cfg shared.PerfConfig, sc perfScenario) PerfResult {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.Log.Info("starting perf scenario",
		"scenario", t.Name(),
		"users", sc.numUsers,
		"container_concurrency", sc.containerConcurrency,
		"max_instances", sc.maxInstances,
		"duration", cfg.Duration,
		"client_concurrency_per_func", sc.clientConcurrency,
	)

	funcs, err := setupPerfFunctions(ctx, h, sc)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer cleanupPerfFunctions(ctx, h, funcs)

	if err := warmupFunctions(ctx, h, funcs); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	h.Log.Info("warmup completed, all containers running")

	var wg sync.WaitGroup
	var totalSuccess atomic.Uint64
	var totalFailure atomic.Uint64
	stopChan := make(chan struct{})

	benchmarkStart := time.Now()

	for _, fn := range funcs {
		for c := 0; c < sc.clientConcurrency; c++ {
			wg.Add(1)
			go func(fn perfFunction) {
				defer wg.Done()
				payload := []byte("perf-payload")
				for {
					select {
					case <-stopChan:
						return
					default:
						reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
						body, status, err := shared.InvokeHTTP(reqCtx, h.Cfg.IngressHTTP, fn.userID, fn.functionID, payload)
						reqCancel()
						if err == nil && status == http.StatusOK && string(body) == string(payload) {
							totalSuccess.Add(1)
						} else {
							totalFailure.Add(1)
						}
					}
				}
			}(fn)
		}
	}

	time.Sleep(cfg.Duration)
	close(stopChan)
	wg.Wait()

	actualDuration := time.Since(benchmarkStart)
	successCount := totalSuccess.Load()
	failureCount := totalFailure.Load()
	totalCount := successCount + failureCount

	var failureRate float64
	if totalCount > 0 {
		failureRate = float64(failureCount) / float64(totalCount)
	}

	totalRPS := float64(successCount) / actualDuration.Seconds()
	avgRPSPerFunc := totalRPS / float64(sc.numUsers)

	result := PerfResult{
		Users:                sc.numUsers,
		ContainerConcurrency: sc.containerConcurrency,
		Duration:             actualDuration,
		SuccessInvokes:       successCount,
		FailureInvokes:       failureCount,
		TotalRPS:             totalRPS,
		AvgRPSPerFunction:    avgRPSPerFunc,
		FailureRate:          failureRate,
	}

	h.Log.Info("perf scenario stats",
		"scenario", t.Name(),
		"users", result.Users,
		"container_concurrency", result.ContainerConcurrency,
		"duration_sec", result.Duration.Seconds(),
		"total_invokes", totalCount,
		"success_invokes", result.SuccessInvokes,
		"failure_invokes", result.FailureInvokes,
		"failure_rate", result.FailureRate,
		"total_rps", result.TotalRPS,
		"avg_rps_per_func", result.AvgRPSPerFunction,
	)

	if failureRate > cfg.MaxFailureRate {
		t.Errorf("failure rate %.2f%% exceeds max %.2f%% (%d failures / %d total)",
			failureRate*100, cfg.MaxFailureRate*100, failureCount, totalCount)
	}

	return result
}

func setupPerfFunctions(ctx context.Context, h *shared.Harness, sc perfScenario) ([]perfFunction, error) {
	funcs := make([]perfFunction, sc.numUsers)
	for i := 0; i < sc.numUsers; i++ {
		uName := fmt.Sprintf("perf-user-%d-%d", sc.containerConcurrency, i)
		user, err := h.CP.CreateUser(ctx, uName)
		if err != nil {
			return nil, fmt.Errorf("create user %s: %w", uName, err)
		}

		spec := shared.EchoFunctionWithScale(
			user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle,
			sc.containerConcurrency, sc.maxInstances,
		)

		fn, err := h.CP.CreateFunction(ctx, user.GetUserId(), spec)
		if err != nil {
			return nil, fmt.Errorf("create function for user %s: %w", uName, err)
		}

		funcs[i] = perfFunction{
			userID:     user.GetUserId(),
			functionID: fn.GetFunctionId(),
		}
	}
	return funcs, nil
}

func warmupFunctions(ctx context.Context, h *shared.Harness, funcs []perfFunction) error {
	payload := []byte("warmup")
	for _, fn := range funcs {
		if err := shared.WaitForInvoke(ctx, h.Cfg, fn.userID, fn.functionID, payload, 45*time.Second); err != nil {
			return fmt.Errorf("warmup failed for user=%d fn=%d: %w", fn.userID, fn.functionID, err)
		}
	}
	return nil
}

func cleanupPerfFunctions(ctx context.Context, h *shared.Harness, funcs []perfFunction) {
	for _, fn := range funcs {
		if err := h.CP.DeleteFunction(ctx, fn.userID, fn.functionID); err != nil {
			h.Log.Error("cleanup: delete function failed", "user_id", fn.userID, "function_id", fn.functionID, "err", err)
		}
		if err := h.CP.DeleteUser(ctx, fn.userID); err != nil {
			h.Log.Error("cleanup: delete user failed", "user_id", fn.userID, "err", err)
		}
	}
}
