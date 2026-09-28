package firecracker

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

func TestNormalizePoolSize(t *testing.T) {
	tests := []struct {
		name     string
		usePool  bool
		poolSize int
		want     int
	}{
		{name: "disabled", usePool: false, poolSize: 128, want: 0},
		{name: "default", usePool: true, poolSize: 0, want: 128},
		{name: "round down", usePool: true, poolSize: 130, want: 128},
		{name: "minimum", usePool: true, poolSize: 8, want: 16},
		{name: "exact multiple", usePool: true, poolSize: 64, want: 64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePoolSize(tc.usePool, tc.poolSize); got != tc.want {
				t.Fatalf("normalizePoolSize(%v, %d) = %d, want %d", tc.usePool, tc.poolSize, got, tc.want)
			}
		})
	}
}

func TestNewAcceptsPoolConfig(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	writeTestFile(t, kernel, "kernel")
	writeTestFile(t, rootfs, "rootfs")

	cfg := Config{
		KernelImagePath: kernel,
		RootfsImagePath: rootfs,
		WorkDir:         filepath.Join(dir, "work"),
		UsePool:         true,
		PoolSize:        64,
		AsyncTeardown:   true,
	}.withDefaults()
	if !cfg.UsePool {
		t.Fatal("expected UsePool to be enabled")
	}
	if cfg.PoolSize != 64 {
		t.Fatalf("PoolSize = %d, want 64", cfg.PoolSize)
	}
	if !cfg.AsyncTeardown {
		t.Fatal("expected AsyncTeardown to be enabled")
	}

	// New() populates the pool synchronously; skip full New here (needs CAP_NET_ADMIN).
	// Verify the network manager accepts the normalized pool size without populate.
	networks, err := newNetworkManager(
		cfg.InternalCIDR,
		cfg.ExposedCIDR,
		cfg.UsePool,
		cfg.PoolSize,
		filepath.Join(cfg.WorkDir, "netns"),
		net.ParseIP(cfg.GuestIP),
		net.ParseIP(cfg.GatewayIP),
		cfg.GuestMAC,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	)
	if err != nil {
		t.Fatalf("newNetworkManager: %v", err)
	}
	if !networks.usePool || networks.poolSize != 64 || networks.pool == nil {
		t.Fatalf("pool manager usePool=%v size=%d pool_nil=%v", networks.usePool, networks.poolSize, networks.pool == nil)
	}
	if cap(networks.pool) != 64 {
		t.Fatalf("pool channel cap = %d, want 64", cap(networks.pool))
	}
}

func TestNewAppliesDefaultsAndPrepareUsesConfiguredRootfs(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	writeTestFile(t, kernel, "kernel")
	writeTestFile(t, rootfs, "rootfs")

	rt, err := New(Config{
		KernelImagePath:     kernel,
		RootfsImagePath:     rootfs,
		WorkDir:             filepath.Join(dir, "work"),
		WorkerListenAddress: "0.0.0.0:50052",
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rt.cfg.FirecrackerBin != defaultFirecrackerBin {
		t.Fatalf("firecracker bin default = %q, want %q", rt.cfg.FirecrackerBin, defaultFirecrackerBin)
	}
	if rt.cfg.RootfsMode != defaultRootfsMode {
		t.Fatalf("rootfs mode default = %q, want %q", rt.cfg.RootfsMode, defaultRootfsMode)
	}
	if rt.cfg.StartTimeout != 60*time.Second {
		t.Fatalf("start timeout = %v, want 60s", rt.cfg.StartTimeout)
	}

	artifact, err := rt.Prepare(context.Background(), &core.FunctionSpec{
		FunctionId: 42,
		Runtime:    &core.RuntimeSpec{Protocol: "grpc"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if artifact.GetImage() != rootfs {
		t.Fatalf("artifact image = %q, want %q", artifact.GetImage(), rootfs)
	}
}

func TestCleanupFailedMachineRemovesSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "instance.socket")
	writeTestFile(t, socket, "stale socket")
	rt := &Runtime{logger: slog.Default()}

	exited, err := rt.cleanupFailedMachine(nil, socket, 1, 2)
	if err != nil {
		t.Fatalf("cleanupFailedMachine: %v", err)
	}
	if !exited {
		t.Fatal("cleanupFailedMachine reported a nil machine as still running")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after cleanup: %v", err)
	}
}

func TestPrepareRejectsMissingRootfs(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	writeTestFile(t, kernel, "kernel")
	writeTestFile(t, rootfs, "rootfs")

	rt, err := New(Config{KernelImagePath: kernel, RootfsImagePath: rootfs, WorkDir: filepath.Join(dir, "work")}, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = rt.Prepare(context.Background(), &core.FunctionSpec{
		FunctionId: 7,
		Runtime:    &core.RuntimeSpec{Image: filepath.Join(dir, "missing.ext4")},
	})
	if err == nil {
		t.Fatal("expected missing rootfs error")
	}
}

func TestNewRejectsSnapshotsWithMutableRootfs(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	writeTestFile(t, kernel, "kernel")
	writeTestFile(t, rootfs, "rootfs")

	_, err := New(Config{
		KernelImagePath: kernel,
		RootfsImagePath: rootfs,
		WorkDir:         filepath.Join(dir, "work"),
		UseSnapshots:    true,
		RootfsMode:      "copy",
	}, slog.Default())
	if err == nil {
		t.Fatal("expected snapshots with copy rootfs to be rejected")
	}
}

func TestPrepareRootfsCopyIsIsolated(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "base.ext4")
	writeTestFile(t, source, "base-rootfs")

	cfg := Config{WorkDir: filepath.Join(dir, "work"), RootfsMode: "copy"}.withDefaults()
	boot, err := prepareBootImage(cfg, source, 99)
	if err != nil {
		t.Fatalf("prepareBootImage: %v", err)
	}
	copyPath := boot.rootfsPath
	if copyPath == source {
		t.Fatal("copy mode returned the shared rootfs path")
	}
	if got, err := os.ReadFile(copyPath); err != nil || string(got) != "base-rootfs" {
		t.Fatalf("copied rootfs = %q, %v", got, err)
	}
	if err := os.WriteFile(copyPath, []byte("mutated"), 0o600); err != nil {
		t.Fatalf("mutate copy: %v", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "base-rootfs" {
		t.Fatalf("source rootfs changed = %q, %v", got, err)
	}
}

func TestBuildFirecrackerConfigInjectsNetworkAndHyperFaaSMetadata(t *testing.T) {
	cfg := Config{
		KernelImagePath:     "/tmp/vmlinux",
		RootfsImagePath:     "/tmp/rootfs.ext4",
		WorkDir:             "/tmp/hyperfaas-fc-test",
		WorkerListenAddress: "0.0.0.0:50052",
	}.withDefaults()
	network := &networkConfig{
		Path:      "/var/run/netns/hyperfaas-test",
		TapName:   tapDeviceName,
		GuestIP:   net.ParseIP("169.254.0.2"),
		GatewayIP: net.ParseIP("169.254.0.1"),
		GuestMAC:  defaultGuestMAC,
		HostIP:    net.ParseIP("10.241.0.1"),
	}
	req := &core.StartSandboxRequest{
		InstanceId: 123,
		WorkerId:   5,
		Function: &core.FunctionSpec{
			FunctionId: 77,
			Runtime: &core.RuntimeSpec{
				Protocol: "grpc",
				Env:      map[string]string{"CUSTOM": "value"},
			},
		},
	}

	machineCfg := buildFirecrackerConfig(cfg, req, network, bootImage{rootfsPath: "/tmp/rootfs.ext4"}, 512, 2)
	if machineCfg.NetNS != network.Path {
		t.Fatalf("NetNS = %q, want %q", machineCfg.NetNS, network.Path)
	}
	if len(machineCfg.NetworkInterfaces) != 1 || !machineCfg.NetworkInterfaces[0].AllowMMDS {
		t.Fatalf("expected one MMDS-enabled network interface")
	}
	if !strings.Contains(machineCfg.KernelArgs, "ip=169.254.0.2::169.254.0.1:255.255.255.252::eth0:off") {
		t.Fatalf("kernel args missing static IP: %s", machineCfg.KernelArgs)
	}
	if !strings.Contains(machineCfg.KernelArgs, "hyperfaas.controller=10.241.0.1:50052") {
		t.Fatalf("kernel args missing controller: %s", machineCfg.KernelArgs)
	}

	encoded := ""
	for _, field := range strings.Fields(machineCfg.KernelArgs) {
		if strings.HasPrefix(field, "hyperfaas.env_b64=") {
			encoded = strings.TrimPrefix(field, "hyperfaas.env_b64=")
		}
	}
	if encoded == "" {
		t.Fatal("kernel args missing env payload")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode env payload: %v", err)
	}
	for _, want := range []string{"CONTROLLER_ADDRESS=10.241.0.1:50052", "INSTANCE_ID=123", "FUNCTION_ID=77", "CUSTOM=value"} {
		if !strings.Contains(string(decoded), want) {
			t.Fatalf("decoded env payload %q missing %q", decoded, want)
		}
	}
}

func writeTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
