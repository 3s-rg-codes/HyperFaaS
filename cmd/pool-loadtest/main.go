package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/workerpb"
)

var (
	workerAddr  = flag.String("worker", "127.0.0.1:50052", "worker gRPC address")
	image       = flag.String("image", "europe-west3-docker.pkg.dev/kollecta-dev/dirigent-images/hfaas-sleep-0000:latest", "function image ref")
	concurrency = flag.Int("concurrency", 32, "parallel CreateSandbox calls per wave")
	waves       = flag.Int("waves", 3, "number of start/stop waves")
	stopDelay   = flag.Duration("stop-delay", 2*time.Second, "delay before stopping sandboxes in a wave")
	timeout     = flag.Duration("timeout", 120*time.Second, "per-request timeout")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	conn, err := grpc.NewClient(*workerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial worker: %w", err)
	}
	defer conn.Close()
	client := workerpb.NewSandboxServiceClient(conn)

	fn := &core.FunctionSpec{
		FunctionId: 1,
		Runtime: &core.RuntimeSpec{
			Image:    *image,
			Protocol: "grpc",
		},
	}

	ctx := context.Background()
	prepCtx, cancel := context.WithTimeout(ctx, *timeout)
	_, err = client.PrepareImage(prepCtx, &workerpb.PrepareImageRequest{Function: fn})
	cancel()
	if err != nil {
		return fmt.Errorf("prepare image: %w", err)
	}
	fmt.Printf("image prepared: %s\n", *image)

	var totalOK, totalFail int64
	var allLatencies []time.Duration
	errorCounts := make(map[string]int)
	var errorMu sync.Mutex

	for wave := 1; wave <= *waves; wave++ {
		fmt.Printf("\n=== wave %d/%d concurrency=%d ===\n", wave, *waves, *concurrency)
		started := make([]uint64, 0, *concurrency)
		var startedMu sync.Mutex
		var wg sync.WaitGroup
		var waveOK, waveFail int64
		latencies := make([]time.Duration, 0, *concurrency)
		var latMu sync.Mutex

		baseID := uint64(wave) * 100000

		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				instanceID := baseID + uint64(idx) + 1
				req := &workerpb.CreateSandboxRequest{
					Request: &core.StartSandboxRequest{
						InstanceId: instanceID,
						WorkerId:   1,
						Function:   fn,
					},
				}
				callCtx, callCancel := context.WithTimeout(ctx, *timeout)
				defer callCancel()
				t0 := time.Now()
				_, err := client.CreateSandbox(callCtx, req)
				elapsed := time.Since(t0)
				latMu.Lock()
				latencies = append(latencies, elapsed)
				latMu.Unlock()
				if err != nil {
					atomic.AddInt64(&waveFail, 1)
					key := classifyError(err)
					errorMu.Lock()
					errorCounts[key]++
					errorMu.Unlock()
					return
				}
				atomic.AddInt64(&waveOK, 1)
				startedMu.Lock()
				started = append(started, instanceID)
				startedMu.Unlock()
			}(i)
		}
		wg.Wait()

		totalOK += waveOK
		totalFail += waveFail
		allLatencies = append(allLatencies, latencies...)
		printStats(fmt.Sprintf("wave %d start", wave), int(waveOK), int(waveFail), latencies)

		time.Sleep(*stopDelay)

		var stopOK, stopFail int64
		var stopWg sync.WaitGroup
		for _, id := range started {
			stopWg.Add(1)
			go func(instanceID uint64) {
				defer stopWg.Done()
				stopCtx, stopCancel := context.WithTimeout(ctx, *timeout)
				defer stopCancel()
				_, err := client.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: instanceID})
				if err != nil {
					atomic.AddInt64(&stopFail, 1)
					key := "stop: " + classifyError(err)
					errorMu.Lock()
					errorCounts[key]++
					errorMu.Unlock()
					return
				}
				atomic.AddInt64(&stopOK, 1)
			}(id)
		}
		stopWg.Wait()
		fmt.Printf("wave %d stop: ok=%d fail=%d\n", wave, stopOK, stopFail)
	}

	fmt.Printf("\n=== totals ===\n")
	fmt.Printf("start ok=%d fail=%d\n", totalOK, totalFail)
	printStats("all waves", int(totalOK), int(totalFail), allLatencies)

	fmt.Println("\nerror breakdown:")
	for key, count := range errorCounts {
		fmt.Printf("  %s: %d\n", key, count)
	}
	return nil
}

func classifyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "cannot assign requested address"):
		return "bind/cannot assign address"
	case strings.Contains(msg, "address already in use"):
		return "bind/address in use"
	case strings.Contains(msg, "CNI"):
		return "cni"
	case strings.Contains(msg, "OCI runtime"), strings.Contains(msg, "shim"), strings.Contains(msg, "runc"):
		return "shim/oci"
	case strings.Contains(msg, "sandbox host port not reachable"):
		return "readiness/host port"
	case strings.Contains(msg, "pool"):
		return "pool"
	case strings.Contains(msg, "pull"):
		return "image pull"
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "DeadlineExceeded"):
		return "timeout"
	default:
		if len(msg) > 120 {
			return msg[:120] + "..."
		}
		return msg
	}
}

func printStats(label string, ok, fail int, latencies []time.Duration) {
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	fmt.Printf("%s: ok=%d fail=%d", label, ok, fail)
	if len(latencies) == 0 {
		fmt.Println()
		return
	}
	p := func(q float64) time.Duration {
		idx := int(float64(len(latencies)-1) * q)
		return latencies[idx]
	}
	fmt.Printf(" | lat p50=%s p90=%s p99=%s max=%s\n", p(0.5), p(0.9), p(0.99), latencies[len(latencies)-1])
}
