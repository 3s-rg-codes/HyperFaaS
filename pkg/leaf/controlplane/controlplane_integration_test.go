//go:build integration

package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"

	"google.golang.org/grpc"
)

const TEST_CONCURRENCY = 10000

func TestControlPlaneColdStartEmitsInstanceChange(t *testing.T) {
	t.Parallel()

	env := setup(t)

	functionID := "function-cold-start"
	env.upsertFunction(functionID)

	env.concurrencyReporter.HandleRequestIn(functionID)
	defer env.concurrencyReporter.HandleRequestOut(functionID)

	select {
	case ic := <-env.instanceChanges:
		if ic.FunctionId != functionID {
			t.Fatalf("unexpected function id. want %s, got %s", functionID, ic.FunctionId)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for instance change event")
	}
}

func TestControlPlaneColdStartConcurrentFunctions(t *testing.T) {

	env := setup(t)

	for i := range TEST_CONCURRENCY {
		env.upsertFunction(fmt.Sprintf("function-%d", i))
	}

	var wg sync.WaitGroup

	for i := range TEST_CONCURRENCY {
		wg.Go(func() {
			fid := fmt.Sprintf("function-%d", i)
			env.concurrencyReporter.HandleRequestIn(fid)
			time.Sleep(time.Duration(rand.Intn(250)) * time.Millisecond)
			env.concurrencyReporter.HandleRequestOut(fid)
		})
	}

	seen := make(map[string]bool, TEST_CONCURRENCY)

	timeout := time.After(5 * time.Second)
	for i := range TEST_CONCURRENCY {
		select {
		case ic := <-env.instanceChanges:
			seen[ic.FunctionId] = true
		case <-timeout:
			t.Fatalf("timed out waiting for instance change event %d/%d", i+1, TEST_CONCURRENCY)
		}
	}

	wg.Wait()

	for i := range TEST_CONCURRENCY {
		fid := fmt.Sprintf("function-%d", i)
		if !seen[fid] {
			t.Fatalf("missing instance change for function %s", fid)
		}
	}
}

type controlPlaneTestEnv struct {
	ctx                 context.Context
	cp                  *ControlPlane
	concurrencyReporter *metrics.ConcurrencyReporter
	instanceChanges     chan metrics.InstanceChange
}

func setup(t *testing.T) *controlPlaneTestEnv {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	workerAddr := startMockWorkerServer(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg := config.Config{
		WorkerAddresses:       []string{workerAddr},
		Containerized:         true,
		MaxInstancesPerWorker: 2,
		ScaleToZeroAfter:      time.Minute,
		StartTimeout:          time.Second,
		StopTimeout:           time.Second,
		CallTimeout:           time.Second,
		StatusBackoff:         time.Second,
	}
	cfg.ApplyDefaults()

	workerClient, err := dataplane.NewWorkerClient(ctx, 0, workerAddr, cfg, logger)
	if err != nil {
		t.Fatalf("failed to create worker client: %v", err)
	}

	instanceChanges := make(chan metrics.InstanceChange, TEST_CONCURRENCY)
	functionScaleEvents := make(chan metrics.ZeroScaleEvent, 1)
	metricChan := make(chan metrics.MetricEvent, TEST_CONCURRENCY)
	concurrencyReporter := metrics.NewConcurrencyReporter(logger, metricChan, time.Second)

	cp := NewControlPlane(ctx, cfg, logger, instanceChanges, []*dataplane.WorkerClient{workerClient}, functionScaleEvents, concurrencyReporter)

	go cp.Run(ctx)

	return &controlPlaneTestEnv{
		ctx:                 ctx,
		cp:                  cp,
		concurrencyReporter: concurrencyReporter,
		instanceChanges:     instanceChanges,
	}
}

func (e *controlPlaneTestEnv) upsertFunction(functionID string) {
	e.cp.UpsertFunction(&metadata.FunctionMetadata{
		ID: functionID,
		Config: &common.Config{
			Memory:         64 * 1024 * 1024,
			MaxConcurrency: 100,
			Timeout:        15,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  50000,
			},
		},
		Image: &common.Image{Tag: "test:latest"},
	})
}

func startMockWorkerServer(t *testing.T) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	workerpb.RegisterWorkerServer(server, &controller.MockController{})

	go func() {
		if err := server.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("mock worker server exited: %v", err)
		}
	}()

	t.Cleanup(server.Stop)

	return lis.Addr().String()
}
