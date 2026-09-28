package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

// benchLogger drops everything below error so eviction logging does not distort
// the benchmarked data-structure cost.
func benchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// benchSandboxWithInstances builds a Sandbox with n ready instances spread over
// 30 functions, matching the GCE HyperFaaS envelope.
func benchSandboxWithInstances(n int) *Sandbox {
	s := &Sandbox{
		logger:       benchLogger(),
		readySignals: NewReadySignals(),
		instances:    make(map[uint64]*sandboxRecord, n),
		removalsSubs: make(map[int]chan SandboxRemoval),
	}
	for i := 0; i < n; i++ {
		state := &core.InstanceState{
			InstanceId:          uint64(1<<62) + uint64(i),
			FunctionId:          uint64(i%30 + 1),
			WorkerId:            1,
			Address:             fmt.Sprintf("10.0.0.5:%d", 30000+i),
			Protocol:            "http",
			Ready:               true,
			WorkerStateRevision: uint64(i + 1),
		}
		s.instances[state.InstanceId] = &sandboxRecord{state: state}
	}
	s.stateRevision = uint64(n)
	return s
}

// BenchmarkSandboxStateSnapshot measures the fast identity snapshot the leaf
// consumes on every WatchState relist tick. It must stay O(instances) and avoid
// per-sandbox runtime calls.
func BenchmarkSandboxStateSnapshot(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("instances=%d", n), func(b *testing.B) {
			s := benchSandboxWithInstances(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				instances, revision := s.SandboxStateSnapshot()
				if len(instances) != n || revision != uint64(n) {
					b.Fatalf("snapshot len=%d revision=%d, want %d/%d", len(instances), revision, n, n)
				}
			}
		})
	}
}

// BenchmarkSandboxRemovalBroadcast measures the O(1) per-removal fan-out that
// replaces full-snapshot pushes on every lifecycle transition. The "consumed"
// case keeps the subscriber draining; the "dropped" case backs up the buffer so
// the non-blocking drop path is exercised.
func BenchmarkSandboxRemovalBroadcast(b *testing.B) {
	for _, subscribers := range []int{1, 4} {
		b.Run(fmt.Sprintf("subscribers=%d/consumed", subscribers), func(b *testing.B) {
			s := benchSandboxWithInstances(0)
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			for i := 0; i < subscribers; i++ {
				ch := s.SubscribeSandboxRemovals(ctx)
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range ch {
					}
				}()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.broadcastRemoval(SandboxRemoval{InstanceID: uint64(i), Revision: uint64(i)})
			}
			b.StopTimer()
			cancel()
			wg.Wait()
		})

		b.Run(fmt.Sprintf("subscribers=%d/dropped", subscribers), func(b *testing.B) {
			s := benchSandboxWithInstances(0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for i := 0; i < subscribers; i++ {
				_ = s.SubscribeSandboxRemovals(ctx)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.broadcastRemoval(SandboxRemoval{InstanceID: uint64(i), Revision: uint64(i)})
			}
		})
	}
}
