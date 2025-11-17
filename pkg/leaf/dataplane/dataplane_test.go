//go:build unit

package dataplane

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

const (
	TEST_FUNCTION_MAX_CONCURRENCY = 25000
	TEST_FUNCTION_TIMEOUT         = 15
	CONCURRENCY_REPORT_INTERVAL   = 1 * time.Second
	TEST_TIMEOUT                  = 5 * time.Second
	TEST_CONCURRENCY              = 10000
)

type testMdClient struct {
}

func (c *testMdClient) GetFunction(ctx context.Context, functionId string) (*metadata.FunctionMetadata, error) {
	return &metadata.FunctionMetadata{
		ID: functionId,
		Image: &common.Image{
			Tag: "test",
		},
		Config: &common.Config{
			Memory: 100 * 1024 * 1024,
			Cpu: &common.CPUConfig{
				Period: 100000,
				Quota:  50000,
			},
			MaxConcurrency: TEST_FUNCTION_MAX_CONCURRENCY,
			Timeout:        TEST_FUNCTION_TIMEOUT,
		},
	}, nil
}

type testClient struct {
	counter atomic.Int32
}

func setup() (*DataPlane, *testClient, chan metrics.InstanceChange, *metrics.ConcurrencyReporter) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	instanceChangesChan := make(chan metrics.InstanceChange)
	metricChan := make(chan metrics.MetricEvent)
	cr := metrics.NewConcurrencyReporter(logger, metricChan, CONCURRENCY_REPORT_INTERVAL)
	dp := NewDataPlane(logger, &testMdClient{}, instanceChangesChan, cr)

	client := &testClient{
		counter: atomic.Int32{},
	}
	return dp, client, instanceChangesChan, cr
}

func TestDataPlaneTryConcurrency(t *testing.T) {
	dp, client, icc, cr := setup()
	ctx, cancel := context.WithTimeout(context.Background(), TEST_TIMEOUT)
	defer cancel()

	wg := sync.WaitGroup{}

	t.Logf("Starting data plane")

	dpCtx, dpCancel := context.WithCancel(ctx)
	go dp.Run(dpCtx)
	go cr.Run(dpCtx)
	go drainMetricChan(dpCtx, cr.GetMetricChan())
	defer dpCancel()

	t.Logf("Starting instance changes")

	wg.Go(func() { simulateInstanceChanges(t, "test", icc) })

	t.Logf("Starting %d calls", TEST_CONCURRENCY)

	for range TEST_CONCURRENCY {
		wg.Go(func() {
			dp.Try(ctx, "test", func(address string) error {
				client.counter.Add(1)
				return nil
			})
		})
	}

	t.Logf("Waiting for calls to complete")
	wg.Wait()
	t.Logf("Calls completed")

	if client.counter.Load() != TEST_CONCURRENCY {
		t.Errorf("expected %d calls, got %d", TEST_CONCURRENCY, client.counter.Load())
	}

}

func TestDataPlaneScaleFromZero(t *testing.T) {
	dp, client, icc, cr := setup()
	ctx, cancel := context.WithTimeout(context.Background(), TEST_TIMEOUT)
	defer cancel()

	wg := sync.WaitGroup{}

	dpCtx, dpCancel := context.WithCancel(ctx)
	go dp.Run(dpCtx)
	go cr.Run(dpCtx)
	go simulateControlPlane(t, dpCtx, cr.GetMetricChan(), icc)
	defer dpCancel()

	t.Logf("Starting %d calls", TEST_CONCURRENCY)
	for range TEST_CONCURRENCY {
		wg.Go(func() {
			dp.Try(ctx, "test", func(address string) error {
				client.counter.Add(1)
				return nil
			})
		})
	}

	t.Logf("Waiting for calls to complete")
	wg.Wait()
	t.Logf("Calls completed")

	if client.counter.Load() != TEST_CONCURRENCY {
		t.Errorf("expected %d calls, got %d", TEST_CONCURRENCY, client.counter.Load())
	}
}

func simulateInstanceChanges(t *testing.T, fid string, icc chan metrics.InstanceChange) {
	minLife := 50
	maxLife := 100
	n := 100

	wg := sync.WaitGroup{}
	for range n {
		wg.Go(func() {

			name := strconv.Itoa(rand.Intn(10000000))
			time.Sleep(time.Duration(rand.Intn(maxLife-minLife)) + time.Duration(minLife)*time.Millisecond)
			icc <- metrics.InstanceChange{
				FunctionId: fid,
				Address:    name,
				Have:       true,
			}

			time.Sleep(time.Duration(rand.Intn(maxLife-minLife)) + time.Duration(minLife)*time.Millisecond)
			icc <- metrics.InstanceChange{
				FunctionId: fid,
				Address:    name,
				Have:       false,
			}

		})
	}
	t.Logf("Waiting for instance changes to complete")
	wg.Wait()
	t.Logf("Instance changes completed")
}

func drainMetricChan(ctx context.Context, c chan metrics.MetricEvent) {
	for {
		select {
		case <-c:
		case <-ctx.Done():
			return
		}
	}
}

// simulateControlPlane simulates the control plane receiving metric events and scaling the function.
// fails the test if the function is scaled again after already being scaled.
func simulateControlPlane(t *testing.T, ctx context.Context, mc chan metrics.MetricEvent, icc chan metrics.InstanceChange) {
	minLife := 50
	maxLife := 100
	// after this many requests, we start to add extra instances.
	extraInstanceAfter := int32(250)
	counter := atomic.Int32{}
	scaled := false
	for {
		select {
		case <-ctx.Done():
			return
		case metricEvent := <-mc:
			go func(ev metrics.MetricEvent) {
				if ev.ColdStart {
					if scaled {
						t.Errorf("function %s was scaled again after already being scaled", ev.FunctionId)
					}
					scaled = true
					t.Logf("cold start detected for function %s", ev.FunctionId)
					time.Sleep(time.Duration(rand.Intn(maxLife-minLife)) + time.Duration(minLife)*time.Millisecond)
					icc <- metrics.InstanceChange{
						FunctionId: ev.FunctionId,
						Address:    strconv.Itoa(rand.Intn(10000000)),
						Have:       true,
					}
				} else {
					counter.Add(1)
					if counter.Load() > extraInstanceAfter {
						counter.Store(0)
						icc <- metrics.InstanceChange{
							FunctionId: ev.FunctionId,
							Address:    strconv.Itoa(rand.Intn(10000000)),
							Have:       true,
						}
					}
				}
			}(metricEvent)
		}
	}
}
