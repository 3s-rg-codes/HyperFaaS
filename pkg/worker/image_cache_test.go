package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime/fake"
)

func TestPrepareImageAppearsInCachedImagesAndCurrentState(t *testing.T) {
	cfg := WorkerConfig{}
	cfg.Runtime.Type = "fake"
	cfg.Stats.MetricsInterval = time.Hour
	sb, err := NewSandbox(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	fn := &core.FunctionSpec{
		FunctionId: 7,
		Runtime:    &core.RuntimeSpec{Image: fake.ImageEchoHTTP, Protocol: "http"},
	}
	if _, err := sb.PrepareImage(context.Background(), fn); err != nil {
		t.Fatalf("PrepareImage: %v", err)
	}
	cached := sb.CachedImages()
	if len(cached) == 0 {
		t.Fatal("expected cached images after PrepareImage")
	}
	found := false
	for _, img := range cached {
		if img.GetImage() == fake.ImageEchoHTTP {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cached images=%v missing %s", cached, fake.ImageEchoHTTP)
	}

	health := NewProcHealthService(cfg, sb)
	state, err := health.CurrentState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.GetCachedImages()) == 0 {
		t.Fatal("CurrentState missing cached_images")
	}
}
