//go:build integration

package firecracker_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/worker"
	fcruntime "hyperfaas-ideal-arch/pkg/worker/runtime/firecracker"
	workerpb "hyperfaas-ideal-arch/pkg/workerpb"
	"hyperfaas-ideal-arch/test/shared"
)

func TestIntegrationStartStatsStop(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("firecracker integration test requires root for netns/tap setup")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("/dev/kvm is not available")
	}
	kernel := os.Getenv("HYPERFAAS_FIRECRACKER_KERNEL")
	rootfs := os.Getenv("HYPERFAAS_FIRECRACKER_ROOTFS")
	if kernel == "" || rootfs == "" {
		t.Skip("set HYPERFAAS_FIRECRACKER_KERNEL and HYPERFAAS_FIRECRACKER_ROOTFS")
	}

	rt, err := fcruntime.New(fcruntime.Config{
		KernelImagePath:     kernel,
		RootfsImagePath:     rootfs,
		WorkDir:             filepath.Join(t.TempDir(), "work"),
		WorkerListenAddress: "0.0.0.0:50052",
		RootfsMode:          "copy",
		StartTimeout:        20 * time.Second,
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, err := rt.Start(ctx, &core.StartSandboxRequest{
		InstanceId: 9001,
		WorkerId:   7,
		Function: &core.FunctionSpec{
			FunctionId: 99,
			Runtime: &core.RuntimeSpec{
				Image:    rootfs,
				Protocol: "grpc",
				Resources: &core.ResourceSpec{
					MemoryBytes: 256 * 1024 * 1024,
					CpuUnits:    1000,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if state.GetAddress() == "" {
		t.Fatal("expected proxy address")
	}
	if _, err := rt.Stats(ctx, 9001); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if err := rt.Stop(ctx, 9001); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestIntegrationEchoGRPCEndToEnd(t *testing.T) {
	requireFirecrackerHost(t)
	kernel := os.Getenv("HYPERFAAS_FIRECRACKER_KERNEL")
	rootfs := os.Getenv("HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	if kernel == "" || rootfs == "" {
		t.Skip("set HYPERFAAS_FIRECRACKER_KERNEL and HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	}

	port := freePort(t)
	workDir := filepath.Join(t.TempDir(), "work")
	cfg := worker.WorkerConfig{
		NodeID: "firecracker-test-worker",
		Logging: config.LoggingConfig{
			Level:  "error",
			Format: "text",
		},
		Server: config.ServerConfig{
			ListenAddress: net.JoinHostPort("0.0.0.0", port),
		},
		Runtime: worker.RuntimeConfig{
			Type: "firecracker",
			Firecracker: worker.FirecrackerConfig{
				KernelImagePath: kernel,
				RootfsImagePath: rootfs,
				RootfsMode:      "copy",
				WorkDir:         workDir,
				Debug:           true,
			},
		},
		Stats: worker.StatsConfig{
			UpdateBufferSize: 1000,
			MetricsInterval:  time.Second,
			BudgetCPU:        2,
			BudgetMemory:     2 * 1024 * 1024 * 1024,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := worker.NewServer(cfg, logger)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	if !waitForTCPPort(net.JoinHostPort("127.0.0.1", port), 10*time.Second) {
		cancel()
		t.Fatalf("worker did not listen on %s", port)
	}
	defer func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
	}()

	conn, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial worker: %v", err)
	}
	defer conn.Close()
	workerClient := workerpb.NewSandboxServiceClient(conn)

	function := &core.FunctionSpec{
		UserId:     1,
		FunctionId: 4242,
		Runtime: &core.RuntimeSpec{
			Image:    rootfs,
			Protocol: "grpc",
			Resources: &core.ResourceSpec{
				MemoryBytes: 256 * 1024 * 1024,
				CpuUnits:    1000,
			},
		},
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer startCancel()
	if _, err := workerClient.PrepareImage(startCtx, &workerpb.PrepareImageRequest{Function: function}); err != nil {
		t.Fatalf("PrepareImage: %v", err)
	}
	state, err := workerClient.CreateSandbox(startCtx, &workerpb.CreateSandboxRequest{Request: &core.StartSandboxRequest{
		Function:   function,
		WorkerId:   9,
		InstanceId: 7001,
	}})
	if err != nil {
		dumpFirecrackerLogs(t, workDir, 7001)
		t.Fatalf("CreateSandbox: %v", err)
	}
	if !state.GetReady() {
		t.Fatal("expected ready firecracker sandbox")
	}
	if state.GetAddress() == "" {
		t.Fatal("expected sandbox address")
	}

	echoConn, err := grpc.NewClient(state.GetAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial echo: %v", err)
	}
	defer echoConn.Close()
	echoClient := echopb.NewEchoClient(echoConn)
	resp, err := echoClient.Echo(startCtx, &echopb.EchoRequest{Data: []byte("firecracker-e2e")})
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if string(resp.GetData()) != "firecracker-e2e" {
		t.Fatalf("echo mismatch: %q", resp.GetData())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if _, err := workerClient.StopSandbox(stopCtx, &workerpb.StopSandboxRequest{InstanceId: 7001}); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
}

func TestIntegrationEchoGRPCRuntimeDirect(t *testing.T) {
	requireFirecrackerHost(t)
	kernel := os.Getenv("HYPERFAAS_FIRECRACKER_KERNEL")
	image := os.Getenv("HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	if kernel == "" || image == "" {
		t.Skip("set HYPERFAAS_FIRECRACKER_KERNEL and HYPERFAAS_FIRECRACKER_ECHO_GRPC_ROOTFS")
	}

	listenAddr, readyServer, cleanup := shared.StartFakeReadyServer(t)
	defer cleanup()

	workDir := filepath.Join(t.TempDir(), "work")
	rt, err := fcruntime.New(fcruntime.Config{
		KernelImagePath:     kernel,
		RootfsImagePath:     image,
		RootfsMode:          "copy",
		WorkDir:             workDir,
		WorkerListenAddress: listenAddr,
		Debug:               true,
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	state, err := rt.Start(ctx, &core.StartSandboxRequest{
		InstanceId: 7101,
		WorkerId:   9,
		Function: &core.FunctionSpec{
			FunctionId: 4343,
			Runtime: &core.RuntimeSpec{
				Image:    image,
				Protocol: "grpc",
				Resources: &core.ResourceSpec{
					MemoryBytes: 256 * 1024 * 1024,
					CpuUnits:    1000,
				},
			},
		},
	})
	if err != nil {
		dumpFirecrackerLogs(t, workDir, 7101)
		t.Fatalf("Start: %v", err)
	}
	defer rt.Stop(context.Background(), 7101)

	select {
	case got := <-readyServer.ReadyChannel():
		if got != 7101 {
			t.Fatalf("ready instance = %d, want 7101", got)
		}
	case <-time.After(60 * time.Second):
		if err := tryEcho(context.Background(), state.GetAddress(), []byte("probe-without-ready")); err != nil {
			t.Logf("echo before ready failed: %v", err)
		} else {
			t.Logf("echo before ready succeeded; guest-to-controller callback path is failing")
		}
		dumpNetworkDebug(t, 7101)
		dumpFirecrackerLogs(t, workDir, 7101)
		t.Fatalf("guest did not signal ready; proxy address is %s", state.GetAddress())
	}

	if err := tryEcho(ctx, state.GetAddress(), []byte("direct-firecracker")); err != nil {
		t.Fatalf("Echo: %v", err)
	}
}

func tryEcho(ctx context.Context, address string, payload []byte) error {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	echoConn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer echoConn.Close()
	resp, err := echopb.NewEchoClient(echoConn).Echo(callCtx, &echopb.EchoRequest{Data: payload})
	if err != nil {
		return err
	}
	if string(resp.GetData()) != string(payload) {
		return fmt.Errorf("echo mismatch: %q", resp.GetData())
	}
	return nil
}

func dumpFirecrackerLogs(t *testing.T, workDir string, instanceID uint64) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(workDir, "instances", strconv.FormatUint(instanceID, 10)+".log"),
		filepath.Join(workDir, "instances", strconv.FormatUint(instanceID, 10), "rootfs.ext4"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Logf("debug artifact missing %s: %v", path, err)
			continue
		}
		if info.IsDir() || info.Size() > 1<<20 {
			t.Logf("debug artifact %s size=%d", path, info.Size())
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Logf("read debug artifact %s: %v", path, err)
			continue
		}
		t.Logf("debug artifact %s:\n%s", path, data)
	}
}

func dumpNetworkDebug(t *testing.T, instanceID uint64) {
	t.Helper()
	ns := "hyperfaas-fc-" + strconv.FormatUint(instanceID, 10)
	commands := [][]string{
		{"ip", "addr"},
		{"ip", "route"},
		{"ip", "route", "get", "10.242.0.10"},
		{"ip", "netns", "exec", ns, "ip", "addr"},
		{"ip", "netns", "exec", ns, "ip", "route"},
		{"ip", "netns", "exec", ns, "iptables", "-t", "nat", "-S"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("network debug %v failed: %v\n%s", args, err, out)
			continue
		}
		t.Logf("network debug %v:\n%s", args, out)
	}
}

func requireFirecrackerHost(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("firecracker integration test requires root for netns/tap setup")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("/dev/kvm is not available")
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port listen: %v", err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("invalid port %q: %v", port, err)
	}
	return port
}

func waitForTCPPort(address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
