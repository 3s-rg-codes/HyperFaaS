//go:build unit

package mock

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
)

func newMockRuntimeForTest() *MockRuntime {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewMockRuntime(logger, controller.NewReadySignals(true))
}

func defaultConfig() *commonpb.Config {
	return &commonpb.Config{
		Timeout: 2,
		Memory:  1024 * 1024 * 1024,
		Cpu: &commonpb.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}
}

func TestMockRuntime_Start_TableDriven(t *testing.T) {
	r := newMockRuntimeForTest()

	tests := []struct {
		name      string
		imageTag  string
		wantError bool
	}{
		{name: "hello", imageTag: "hyperfaas-hello:latest"},
		{name: "echo", imageTag: "hyperfaas-echo:latest"},
		{name: "simul", imageTag: "hyperfaas-simul:latest"},
		{name: "invalid", imageTag: "non-existing", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := r.Start(context.Background(), tt.name, tt.imageTag, defaultConfig())
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error for %s, got nil", tt.imageTag)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Id == "" {
				t.Errorf("empty container id")
			}
			if !r.ContainerExists(context.Background(), c.Id) {
				t.Errorf("ContainerExists returned false for running instance %s", c.Id)
			}
		})
	}
}

func TestMockRuntime_Start_Concurrent(t *testing.T) {
	r := newMockRuntimeForTest()
	const n = 1000
	functionID := "xd"

	wg := sync.WaitGroup{}
	errs := make(chan error, n)

	for range n {
		wg.Go(func() {
			_, err := r.Start(context.Background(), functionID, "hyperfaas-hello:latest", defaultConfig())
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("start error: %v", err)
		}
	}

	// Verify we have exactly n instances and unique IDs
	r.mapLock.RLock()
	instances := r.Instances[functionID]
	r.mapLock.RUnlock()

	if len(instances) != n {
		t.Errorf("expected %d instances, got %d", n, len(instances))
	}

	seen := make(map[string]struct{})
	for _, inst := range instances {
		if _, ok := seen[inst.id]; ok {
			t.Errorf("duplicate instance id detected: %s", inst.id)
		}
		seen[inst.id] = struct{}{}
		if !r.ContainerExists(context.Background(), inst.id) {
			t.Errorf("ContainerExists returned false for running instance %s", inst.id)
		}
	}
}

func TestMockRuntime_Stop(t *testing.T) {
	r := newMockRuntimeForTest()
	functionID := "xd"

	// Start 3 instances
	ids := make([]string, 0, 3)
	for range 3 {
		c, err := r.Start(context.Background(), functionID, "hyperfaas-hello:latest", defaultConfig())
		if err != nil {
			t.Errorf("start error: %v", err)
		}
		ids = append(ids, c.Id)
	}

	// Stop the second instance
	if err := r.Stop(context.Background(), ids[1]); err != nil {
		t.Errorf("stop error: %v", err)
	}

	if r.ContainerExists(context.Background(), ids[1]) {
		t.Errorf("ContainerExists returned true after stop for %s", ids[1])
	}

	// Verify the remaining count is 2
	r.mapLock.RLock()
	remaining := len(r.Instances[functionID])
	r.mapLock.RUnlock()
	if remaining != 2 {
		t.Errorf("expected 2 remaining instances, got %d", remaining)
	}

	// Stopping a non-existing instance should error
	if err := r.Stop(context.Background(), "does-not-exist"); err == nil {
		t.Errorf("expected error when stopping non-existing instance")
	}
}

func TestMockRuntime_MonitorContainer_Timeout(t *testing.T) {
	r := newMockRuntimeForTest()
	cfg := &commonpb.Config{
		Timeout: 1,
		Memory:  1024 * 1024 * 1024,
		Cpu: &commonpb.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}

	fnID := "xd"
	c, err := r.Start(context.Background(), fnID, "hyperfaas-hello:latest", cfg)
	if err != nil {
		t.Errorf("start error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	event, err := r.MonitorContainer(ctx, c.Id, fnID)
	if err != nil {
		t.Errorf("monitor error: %v", err)
	}
	if event != cr.ContainerEventTimeout {
		t.Errorf("expected timeout event, got %v", event)
	}
}
