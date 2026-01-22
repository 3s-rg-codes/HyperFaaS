//go:build unit

package metrics

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	mtrcs "github.com/3s-rg-codes/HyperFaaS/pkg/metrics"
)

func TestResourceMetricsHistoryOrder(t *testing.T) {
	store := NewResourceMetricsStore(1)
	store.Update(0, mtrcs.ResourceMetrics{CPUUtilizationPercent: 1})
	store.Update(0, mtrcs.ResourceMetrics{CPUUtilizationPercent: 2})
	store.Update(0, mtrcs.ResourceMetrics{CPUUtilizationPercent: 3})

	buf := make([]mtrcs.ResourceMetrics, 3)
	n := store.History(0, buf)
	if n != 3 {
		t.Fatalf("unexpected history count: %d", n)
	}
	if buf[0].CPUUtilizationPercent != 1 {
		t.Fatalf("unexpected metric[0]: %v", buf[0].CPUUtilizationPercent)
	}
	if buf[1].CPUUtilizationPercent != 2 {
		t.Fatalf("unexpected metric[1]: %v", buf[1].CPUUtilizationPercent)
	}
	if buf[2].CPUUtilizationPercent != 3 {
		t.Fatalf("unexpected metric[2]: %v", buf[2].CPUUtilizationPercent)
	}
}

func TestResourceMetricsCollectorUpdatesStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewResourceMetricsStore(2)
	collector := NewResourceMetricsCollector(store, logger, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go collector.Run(ctx)

	collector.Add(0, mtrcs.ResourceMetrics{CPUUtilizationPercent: 50})
	collector.Add(1, mtrcs.ResourceMetrics{CPUUtilizationPercent: 10})

	time.Sleep(20 * time.Millisecond)

	metrics, ok := store.Latest(1)
	if !ok {
		t.Fatal("expected latest metrics")
	}
	if metrics.CPUUtilizationPercent != 10 {
		t.Fatalf("unexpected cpu percent: %v", metrics.CPUUtilizationPercent)
	}

	best, ok := store.BestWorker()
	if !ok {
		t.Fatal("expected best worker")
	}
	if !best.Ok {
		t.Fatal("expected best worker snapshot")
	}
	if best.Index != 1 {
		t.Fatalf("unexpected best worker: %d", best.Index)
	}
}
