//go:build perf

package runtime_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
	"hyperfaas-ideal-arch/pkg/worker/runtime/docker"
	"hyperfaas-ideal-arch/pkg/worker/runtime/firecracker"
	"hyperfaas-ideal-arch/pkg/worker/runtime/runc"
	"hyperfaas-ideal-arch/test/shared"
)

const (
	defaultDockerImage = "echo-grpc:latest"
	defaultKernelPath  = "/home/ubuntu/firecracker-test/hello-vmlinux.bin"
	defaultInitrdPath  = "/home/ubuntu/hyperfaas-ideal-arch/bin/firecracker/echo-grpc.cpio.gz"
	numIterations      = 5
)

type runMetrics struct {
	prepareDur time.Duration
	startDur   time.Duration
	readyDur   time.Duration
	stopDur    time.Duration
}

func runPerformanceSuite(t *testing.T, name string, rt runtime.Runtime, spec *core.FunctionSpec) {
	t.Helper()

	var metrics []runMetrics
	ctx := context.Background()

	t.Logf("Starting sequential performance suite for %s over %d iterations", name, numIterations)

	for i := 0; i < numIterations; i++ {
		instanceID := uint64(20000 + i)

		// 1. Prepare
		tPrepare := time.Now()
		artifact, err := rt.Prepare(ctx, spec)
		if err != nil {
			t.Fatalf("[%s] Iteration %d: Prepare failed: %v", name, i, err)
		}
		prepareDur := time.Since(tPrepare)

		// 2. Start
		tStart := time.Now()
		state, err := rt.Start(ctx, &core.StartSandboxRequest{
			Function:   spec,
			Artifact:   artifact,
			InstanceId: instanceID,
			WorkerId:   1,
		})
		if err != nil {
			t.Fatalf("[%s] Iteration %d: Start failed: %v", name, i, err)
		}
		startDur := time.Since(tStart)

		// 3. Wait until ready (TCP ping)
		addr := state.GetAddress()
		if addr == "" {
			_ = rt.Stop(ctx, instanceID)
			t.Fatalf("[%s] Iteration %d: empty address returned from Start", name, i)
		}

		tReady := time.Now()
		dialSuccess := false
		maxWait := 15 * time.Second
		deadline := time.Now().Add(maxWait)
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				dialSuccess = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !dialSuccess {
			_ = rt.Stop(ctx, instanceID)
			t.Fatalf("[%s] Iteration %d: sandbox TCP port %s unreachable after %s", name, i, addr, maxWait)
		}
		readyDur := time.Since(tReady)

		// 4. Stop
		tStop := time.Now()
		if err := rt.Stop(ctx, instanceID); err != nil {
			t.Fatalf("[%s] Iteration %d: Stop failed: %v", name, i, err)
		}
		stopDur := time.Since(tStop)

		metrics = append(metrics, runMetrics{
			prepareDur: prepareDur,
			startDur:   startDur,
			readyDur:   readyDur,
			stopDur:    stopDur,
		})
	}

	reportStats(t, name, metrics)
}

func reportStats(t *testing.T, name string, metrics []runMetrics) {
	t.Helper()

	var sumPrep, sumStart, sumReady, sumStop, sumCold time.Duration
	var minPrep, minStart, minReady, minStop, minCold time.Duration
	var maxPrep, maxStart, maxReady, maxStop, maxCold time.Duration

	for i, m := range metrics {
		coldStart := m.prepareDur + m.startDur + m.readyDur
		if i == 0 {
			minPrep, maxPrep = m.prepareDur, m.prepareDur
			minStart, maxStart = m.startDur, m.startDur
			minReady, maxReady = m.readyDur, m.readyDur
			minStop, maxStop = m.stopDur, m.stopDur
			minCold, maxCold = coldStart, coldStart
		} else {
			if m.prepareDur < minPrep {
				minPrep = m.prepareDur
			}
			if m.prepareDur > maxPrep {
				maxPrep = m.prepareDur
			}
			if m.startDur < minStart {
				minStart = m.startDur
			}
			if m.startDur > maxStart {
				maxStart = m.startDur
			}
			if m.readyDur < minReady {
				minReady = m.readyDur
			}
			if m.readyDur > maxReady {
				maxReady = m.readyDur
			}
			if m.stopDur < minStop {
				minStop = m.stopDur
			}
			if m.stopDur > maxStop {
				maxStop = m.stopDur
			}
			if coldStart < minCold {
				minCold = coldStart
			}
			if coldStart > maxCold {
				maxCold = coldStart
			}
		}

		sumPrep += m.prepareDur
		sumStart += m.startDur
		sumReady += m.readyDur
		sumStop += m.stopDur
		sumCold += coldStart
	}

	n := time.Duration(len(metrics))
	fmt.Printf("\n==================== %s RUNTIME PERFORMANCE (N=%d) ====================\n", name, len(metrics))
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Metric", "Min", "Average", "Max")
	fmt.Printf("----------------------------------------------------------------------\n")
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Prepare", minPrep.Round(time.Microsecond), (sumPrep / n).Round(time.Microsecond), maxPrep.Round(time.Microsecond))
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Start (VMM)", minStart.Round(time.Microsecond), (sumStart / n).Round(time.Microsecond), maxStart.Round(time.Microsecond))
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Ready (TCP)", minReady.Round(time.Microsecond), (sumReady / n).Round(time.Microsecond), maxReady.Round(time.Microsecond))
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Stop", minStop.Round(time.Microsecond), (sumStop / n).Round(time.Microsecond), maxStop.Round(time.Microsecond))
	fmt.Printf("----------------------------------------------------------------------\n")
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Total ColdStart", minCold.Round(time.Microsecond), (sumCold / n).Round(time.Microsecond), maxCold.Round(time.Microsecond))
	fmt.Printf("======================================================================\n\n")
}

func runConcurrencyTest(t *testing.T, name string, rt runtime.Runtime, spec *core.FunctionSpec, concurrency int) {
	t.Helper()

	var wg sync.WaitGroup
	ctx := context.Background()

	artifact, err := rt.Prepare(ctx, spec)
	if err != nil {
		t.Fatalf("[%s-Concurrent] Prepare failed: %v", name, err)
	}

	startTimes := make([]time.Time, concurrency)
	readyTimes := make([]time.Time, concurrency)
	stopTimes := make([]time.Time, concurrency)
	errors := make([]error, concurrency)

	tBegin := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			instanceID := uint64(30000 + idx)

			startTimes[idx] = time.Now()
			// 1. Start
			state, err := rt.Start(ctx, &core.StartSandboxRequest{
				Function:   spec,
				Artifact:   artifact,
				InstanceId: instanceID,
				WorkerId:   1,
			})
			if err != nil {
				errors[idx] = fmt.Errorf("Start failed: %w", err)
				return
			}

			// 2. Wait until ready (TCP ping)
			addr := state.GetAddress()
			dialSuccess := false
			maxWait := 30 * time.Second
			deadline := time.Now().Add(maxWait)
			for time.Now().Before(deadline) {
				conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					dialSuccess = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}

			if !dialSuccess {
				_ = rt.Stop(ctx, instanceID)
				errors[idx] = fmt.Errorf("sandbox TCP unreachable at %s after %s", addr, maxWait)
				return
			}
			readyTimes[idx] = time.Now()

			// 3. Stop
			if err := rt.Stop(ctx, instanceID); err != nil {
				errors[idx] = fmt.Errorf("Stop failed: %w", err)
				return
			}
			stopTimes[idx] = time.Now()
		}(i)
	}

	wg.Wait()
	totalDur := time.Since(tBegin)

	// Check if any errors occurred
	var firstErr error
	errCount := 0
	for _, err := range errors {
		if err != nil {
			errCount++
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if firstErr != nil {
		t.Fatalf("[%s-Concurrent-%d] Failed with %d errors, first error: %v", name, concurrency, errCount, firstErr)
	}

	// Calculate and report metrics
	var totalColdStartSum time.Duration
	var minColdStart, maxColdStart time.Duration

	for i := 0; i < concurrency; i++ {
		coldStart := readyTimes[i].Sub(startTimes[i])
		if i == 0 {
			minColdStart, maxColdStart = coldStart, coldStart
		} else {
			if coldStart < minColdStart {
				minColdStart = coldStart
			}
			if coldStart > maxColdStart {
				maxColdStart = coldStart
			}
		}
		totalColdStartSum += coldStart
	}

	fmt.Printf("[%s-Concurrent-%d] Success. Total test time: %-10s. ColdStart per sandbox: Min=%-10s, Avg=%-10s, Max=%-10s\n",
		name, concurrency, totalDur.Round(time.Millisecond),
		minColdStart.Round(time.Millisecond),
		(totalColdStartSum / time.Duration(concurrency)).Round(time.Millisecond),
		maxColdStart.Round(time.Millisecond),
	)
}

func TestPerfDocker(t *testing.T) {
	imageName := os.Getenv("HYPERFAAS_DOCKER_IMAGE")
	if imageName == "" {
		imageName = defaultDockerImage
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	rt, err := docker.New(docker.Config{
		AutoRemove:          true,
		NetworkName:         "bridge",
		WorkerListenAddress: listenAddr,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create Docker runtime: %v", err)
	}

	has, err := rt.HasImage(context.Background(), imageName)
	if err != nil {
		t.Fatalf("failed to check Docker image %s: %v", imageName, err)
	}
	if !has {
		t.Fatalf("Docker image %q must exist locally prior to running performance tests.", imageName)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 101,
		Runtime: &core.RuntimeSpec{
			Image:    imageName,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "DOCKER", rt, spec)
}

func TestPerfDockerConcurrent(t *testing.T) {
	imageName := os.Getenv("HYPERFAAS_DOCKER_IMAGE")
	if imageName == "" {
		imageName = defaultDockerImage
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	rt, err := docker.New(docker.Config{
		AutoRemove:          true,
		NetworkName:         "bridge",
		WorkerListenAddress: listenAddr,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create Docker runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 101,
		Runtime: &core.RuntimeSpec{
			Image:    imageName,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				// Limit memory to 64MB per sandbox to prevent host OOM under high concurrency
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== DOCKER CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "DOCKER", rt, spec, concurrency)
	}
	fmt.Printf("=======================================================================\n\n")
}

func TestPerfFirecracker(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("Firecracker performance tests require root privileges. Please re-run with sudo.")
	}

	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatalf("/dev/kvm is not accessible. Firecracker requires KVM support: %v", err)
	}

	kernelPath := os.Getenv("HYPERFAAS_FIRECRACKER_KERNEL")
	if kernelPath == "" {
		kernelPath = defaultKernelPath
	}
	if _, err := os.Stat(kernelPath); err != nil {
		t.Fatalf("Firecracker kernel not found at %s. Please verify setup: %v", kernelPath, err)
	}

	initrdPath := os.Getenv("HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	if initrdPath == "" {
		initrdPath = defaultInitrdPath
	}
	if _, err := os.Stat(initrdPath); err != nil {
		t.Fatalf("Firecracker initrd/rootfs not found at %s: %v", initrdPath, err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	rt, err := firecracker.New(firecracker.Config{
		FirecrackerBin:      "firecracker",
		KernelImagePath:     kernelPath,
		RootfsImagePath:     initrdPath,
		RootfsMode:          "copy",
		WorkDir:             filepath.Join(t.TempDir(), "fc-work"),
		WorkerListenAddress: listenAddr,
		StartTimeout:        15 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create Firecracker runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 102,
		Runtime: &core.RuntimeSpec{
			Image:    initrdPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "FIRECRACKER", rt, spec)
}

func TestPerfFirecrackerConcurrent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("Firecracker performance tests require root privileges. Please re-run with sudo.")
	}

	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatalf("/dev/kvm is not accessible. Firecracker requires KVM support: %v", err)
	}

	kernelPath := os.Getenv("HYPERFAAS_FIRECRACKER_KERNEL")
	if kernelPath == "" {
		kernelPath = defaultKernelPath
	}
	initrdPath := os.Getenv("HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	if initrdPath == "" {
		initrdPath = defaultInitrdPath
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	rt, err := firecracker.New(firecracker.Config{
		FirecrackerBin:      "firecracker",
		KernelImagePath:     kernelPath,
		RootfsImagePath:     initrdPath,
		RootfsMode:          "copy",
		WorkDir:             filepath.Join(t.TempDir(), "fc-work-concurrent"),
		WorkerListenAddress: listenAddr,
		StartTimeout:        30 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create Firecracker runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 102,
		Runtime: &core.RuntimeSpec{
			Image:    initrdPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				// Limit memory to 64MB per microVM to prevent host OOM under high concurrency
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== FIRECRACKER CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "FIRECRACKER", rt, spec, concurrency)
	}
	fmt.Printf("============================================================================\n\n")
}

func TestPerfRunc(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary (e.g., from functions/echo-grpc)
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-work"),
		WorkerListenAddress: listenAddr,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 103,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "RUNC", rt, spec)
}

func TestPerfRuncConcurrent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary (e.g., from functions/echo-grpc)
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-work-concurrent"),
		WorkerListenAddress: listenAddr,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 103,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== RUNC CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "RUNC", rt, spec, concurrency)
	}
	fmt.Printf("=====================================================================\n\n")
}

func TestPerfRuncWithNetworkIsolation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-netns-work"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 104,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "RUNC-ISOLATED", rt, spec)
}

func TestPerfRuncConcurrentWithNetworkIsolation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-netns-work-concurrent"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 104,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== RUNC NETWORK ISOLATED CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "RUNC-ISOLATED", rt, spec, concurrency)
	}
	fmt.Printf("======================================================================================\n\n")
}

func TestPerfRuncWithIpvlan(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-ipvlan-work"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
		NetworkMode:         "ipvlan",
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 105,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "RUNC-IPVLAN", rt, spec)
}

func TestPerfRuncConcurrentWithIpvlan(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-ipvlan-work-concurrent"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
		NetworkMode:         "ipvlan",
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 105,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== RUNC IPVLAN CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "RUNC-IPVLAN", rt, spec, concurrency)
	}
	fmt.Printf("============================================================================\n\n")
}

func TestPerfRuncWithPool(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-pool-work"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
		NetworkMode:         "veth",
		UsePool:             true,
		PoolSize:            30,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	// Wait a bit for the pool to be pre-populated
	time.Sleep(1 * time.Second)

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 106,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	runPerformanceSuite(t, "RUNC-POOL", rt, spec)
}

func TestPerfRuncConcurrentWithPool(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("runc tests require root privileges. Please re-run with sudo.")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenAddr, _, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	// Locate the compiled function binary
	binaryPath := "/home/ubuntu/hyperfaas-ideal-arch/bin/echo-grpc-binary" 

	rt, err := runc.New(runc.Config{
		WorkDir:             filepath.Join(t.TempDir(), "runc-pool-work-concurrent"),
		WorkerListenAddress: listenAddr,
		NetworkIsolation:    true,
		NetworkMode:         "veth",
		UsePool:             true,
		PoolSize:            35,
	}, logger)
	if err != nil {
		t.Fatalf("failed to create runc runtime: %v", err)
	}

	// Wait a bit for the pool to populate
	time.Sleep(2 * time.Second)

	spec := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 106,
		Runtime: &core.RuntimeSpec{
			Image:    binaryPath,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 64 * 1024 * 1024,
				CpuUnits:    100,
			},
		},
	}

	fmt.Printf("\n==================== RUNC POOL CONCURRENT PERFORMANCE ====================\n")
	for _, concurrency := range []int{10, 20, 30} {
		runConcurrencyTest(t, "RUNC-POOL", rt, spec, concurrency)
	}
	fmt.Printf("==========================================================================\n\n")
}


