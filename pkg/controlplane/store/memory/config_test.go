package memory

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/core"
)

func availableCapacityConfig() *core.PlatformConfig {
	return &core.PlatformConfig{
		Routing: &core.RoutingPolicyConfig{
			Policy: &core.RoutingPolicyConfig_AvailableCapacity{
				AvailableCapacity: &core.AvailableCapacityRoutingPolicy{},
			},
		},
	}
}

func boundedLoadsConfig(bound float64) *core.PlatformConfig {
	return &core.PlatformConfig{
		Routing: &core.RoutingPolicyConfig{
			Policy: &core.RoutingPolicyConfig_BoundedLoads{
				BoundedLoads: &core.BoundedLoadsRoutingPolicy{Bound: bound, MaxChainLen: 3},
			},
		},
	}
}

func TestPlatformConfigNotFoundBeforeFirstWrite(t *testing.T) {
	s := New()
	defer s.Close()

	if _, err := s.GetPlatformConfig(context.Background()); status.Code(err) != codes.NotFound {
		t.Fatalf("GetPlatformConfig error = %v, want NotFound", err)
	}
}

func TestPlatformConfigRoundTripAssignsMonotonicVersion(t *testing.T) {
	s := New()
	defer s.Close()
	ctx := context.Background()

	first, err := s.PutPlatformConfig(ctx, availableCapacityConfig(), 0)
	if err != nil {
		t.Fatalf("PutPlatformConfig: %v", err)
	}
	if first.GetVersion() != 1 {
		t.Fatalf("first version = %d, want 1", first.GetVersion())
	}

	second, err := s.PutPlatformConfig(ctx, boundedLoadsConfig(1.5), first.GetVersion())
	if err != nil {
		t.Fatalf("PutPlatformConfig: %v", err)
	}
	if second.GetVersion() <= first.GetVersion() {
		t.Fatalf("versions not monotonic: %d then %d", first.GetVersion(), second.GetVersion())
	}

	got, err := s.GetPlatformConfig(ctx)
	if err != nil {
		t.Fatalf("GetPlatformConfig: %v", err)
	}
	if got.GetRouting().GetBoundedLoads().GetBound() != 1.5 {
		t.Fatalf("bound = %v, want 1.5", got.GetRouting().GetBoundedLoads().GetBound())
	}
	if got.GetVersion() != second.GetVersion() {
		t.Fatalf("stored version = %d, want %d", got.GetVersion(), second.GetVersion())
	}
}

func TestPlatformConfigRejectsStaleExpectedVersion(t *testing.T) {
	s := New()
	defer s.Close()
	ctx := context.Background()

	first, err := s.PutPlatformConfig(ctx, availableCapacityConfig(), 0)
	if err != nil {
		t.Fatalf("PutPlatformConfig: %v", err)
	}
	if _, err := s.PutPlatformConfig(ctx, boundedLoadsConfig(1.0), first.GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Fatalf("stale write error = %v, want Aborted", err)
	}
}

func TestWatchPlatformConfigEmitsCurrentThenUpdates(t *testing.T) {
	s := New()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := s.PutPlatformConfig(ctx, availableCapacityConfig(), 0); err != nil {
		t.Fatalf("PutPlatformConfig: %v", err)
	}

	events, errs := s.WatchPlatformConfig(ctx)
	if cfg := nextConfig(t, events, errs); cfg.GetRouting().GetAvailableCapacity() == nil {
		t.Fatalf("initial policy = %v, want available-capacity", cfg.GetRouting().GetPolicy())
	}

	if _, err := s.PutPlatformConfig(ctx, boundedLoadsConfig(2.0), 0); err != nil {
		t.Fatalf("PutPlatformConfig: %v", err)
	}
	if cfg := nextConfig(t, events, errs); cfg.GetRouting().GetBoundedLoads().GetBound() != 2.0 {
		t.Fatalf("updated policy = %v, want bounded-loads", cfg.GetRouting().GetPolicy())
	}
}

// TestWatchPlatformConfigNeverDropsFinalVersion covers the latest-wins mailbox:
// a subscriber that does not read while many writes happen must still observe
// the final stored version. Intermediate versions may be skipped.
func TestWatchPlatformConfigNeverDropsFinalVersion(t *testing.T) {
	s := New()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs := s.WatchPlatformConfig(ctx)
	const writes = 20
	for i := 0; i < writes; i++ {
		if _, err := s.PutPlatformConfig(ctx, availableCapacityConfig(), 0); err != nil {
			t.Fatalf("PutPlatformConfig: %v", err)
		}
	}

	deadline := time.After(2 * time.Second)
	var last uint64
	for last < writes {
		select {
		case cfg := <-events:
			last = cfg.GetVersion()
		case err := <-errs:
			t.Fatalf("watch error: %v", err)
		case <-deadline:
			t.Fatalf("timed out; last version %d, want %d", last, writes)
		}
	}
}

func nextConfig(t *testing.T, events <-chan *core.PlatformConfig, errs <-chan error) *core.PlatformConfig {
	t.Helper()
	select {
	case cfg := <-events:
		return cfg
	case err := <-errs:
		t.Fatalf("watch error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for config")
	}
	return nil
}
