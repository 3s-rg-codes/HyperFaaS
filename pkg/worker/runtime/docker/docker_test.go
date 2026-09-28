package docker

import (
	"context"
	"testing"
	"time"
)

func TestDockerStopTimeoutUsesBoundedGracePeriod(t *testing.T) {
	if got := dockerStopTimeoutSeconds(context.Background()); got != 5 {
		t.Fatalf("dockerStopTimeoutSeconds without deadline = %d, want 5", got)
	}
}

func TestDockerStopTimeoutLeavesDeadlineReserve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()

	got := dockerStopTimeoutSeconds(ctx)
	if got < 1 || got > 2 {
		t.Fatalf("dockerStopTimeoutSeconds with 3.5s deadline = %d, want 1..2", got)
	}
}

func TestDockerStopTimeoutKillsImmediatelyWhenDeadlineIsTooClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if got := dockerStopTimeoutSeconds(ctx); got != 0 {
		t.Fatalf("dockerStopTimeoutSeconds with 500ms deadline = %d, want 0", got)
	}
}
