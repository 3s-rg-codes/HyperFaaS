package firecracker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	functionPort = 50052

	defaultFirecrackerBin = "firecracker"
	defaultWorkDir        = "/var/lib/hyperfaas/firecracker"
	defaultSnapshotDir    = "/var/lib/hyperfaas/firecracker/snapshots"
	defaultRootfsMode     = "copy"
	defaultGuestIP        = "169.254.0.2"
	defaultGatewayIP      = "169.254.0.1"
	defaultGuestMAC       = "02:FC:00:00:00:00"
	defaultMMDSAddress    = "169.254.169.254"
	defaultInternalCIDR   = "10.241.0.0/16"
	defaultExposedCIDR    = "10.242.0.0/16"
	defaultMemSizeMiB     = int64(256)
	defaultVCPUCount      = int64(1)
	defaultStartTimeout   = 60 * time.Second
	teardownTimeout       = 60 * time.Second
)

// Config configures the direct Firecracker runtime. This runtime expects a
// Firecracker-compatible kernel and raw root filesystem image; it intentionally
// does not unpack OCI images. A separate firecracker-containerd runtime can own
// OCI/devmapper/guest-agent concerns later without changing this package.
type Config struct {
	FirecrackerBin  string
	KernelImagePath string
	RootfsImagePath string
	InitrdImagePath string
	KernelArgs      string
	WorkDir         string
	SnapshotDir     string
	UseSnapshots    bool
	Debug           bool
	RootfsMode      string // copy or readonly

	GuestIP      string
	GatewayIP    string
	GuestMAC     string
	MMDSAddress  string
	InternalCIDR string
	ExposedCIDR  string

	WorkerListenAddress   string
	ProxyAdvertiseAddress string
	StartTimeout          time.Duration
	ArtifactsBucket       string

	// UsePool enables a pre-built network namespace pool to bypass kernel
	// mount/namespace lock contention during concurrent cold starts.
	UsePool bool
	// PoolSize is the network pool capacity (default 128 when UsePool).
	PoolSize int
	// AsyncTeardown defers VM shutdown and network cleanup to a background goroutine.
	AsyncTeardown bool
}

const (
	defaultPoolSize   = 128
	minPoolSize       = 16
	poolSizeAlignment = 16
	poolIDBase        = 900000
)

// normalizePoolSize rounds pool size down to a multiple of 16 with a minimum of 16.
func normalizePoolSize(usePool bool, poolSize int) int {
	if !usePool {
		return 0
	}
	if poolSize <= 0 {
		poolSize = defaultPoolSize
	}
	poolSize = (poolSize / poolSizeAlignment) * poolSizeAlignment
	if poolSize < minPoolSize {
		poolSize = minPoolSize
	}
	return poolSize
}

func (c Config) withDefaults() Config {
	if c.FirecrackerBin == "" {
		c.FirecrackerBin = defaultFirecrackerBin
	}
	if c.WorkDir == "" {
		c.WorkDir = defaultWorkDir
	}
	if c.SnapshotDir == "" {
		c.SnapshotDir = defaultSnapshotDir
	}
	if c.RootfsMode == "" {
		c.RootfsMode = defaultRootfsMode
	}
	if c.GuestIP == "" {
		c.GuestIP = defaultGuestIP
	}
	if c.GatewayIP == "" {
		c.GatewayIP = defaultGatewayIP
	}
	if c.GuestMAC == "" {
		c.GuestMAC = defaultGuestMAC
	}
	if c.MMDSAddress == "" {
		c.MMDSAddress = defaultMMDSAddress
	}
	if c.InternalCIDR == "" {
		c.InternalCIDR = defaultInternalCIDR
	}
	if c.ExposedCIDR == "" {
		c.ExposedCIDR = defaultExposedCIDR
	}
	if c.StartTimeout <= 0 {
		c.StartTimeout = defaultStartTimeout
	}
	if c.UsePool {
		c.PoolSize = normalizePoolSize(true, c.PoolSize)
	}
	return c
}

func (c Config) validate() error {
	if c.FirecrackerBin == "" {
		return fmt.Errorf("firecracker runtime: firecracker binary is required")
	}
	if c.KernelImagePath == "" {
		return fmt.Errorf("firecracker runtime: kernel image path is required")
	}
	if _, err := os.Stat(c.KernelImagePath); err != nil {
		return fmt.Errorf("firecracker runtime: kernel image %q: %w", c.KernelImagePath, err)
	}
	if c.RootfsImagePath == "" && c.InitrdImagePath == "" {
		return fmt.Errorf("firecracker runtime: rootfs image path or initrd image path is required")
	}
	if c.RootfsImagePath != "" && c.ArtifactsBucket == "" {
		if _, err := os.Stat(c.RootfsImagePath); err != nil {
			return fmt.Errorf("firecracker runtime: rootfs image %q: %w", c.RootfsImagePath, err)
		}
	}
	if c.InitrdImagePath != "" && c.ArtifactsBucket == "" {
		if _, err := os.Stat(c.InitrdImagePath); err != nil {
			return fmt.Errorf("firecracker runtime: initrd image %q: %w", c.InitrdImagePath, err)
		}
	}
	if c.RootfsMode != "copy" && c.RootfsMode != "readonly" {
		return fmt.Errorf("firecracker runtime: rootfs mode must be copy or readonly, got %q", c.RootfsMode)
	}
	if c.UseSnapshots && c.InitrdImagePath == "" && c.RootfsMode != "readonly" {
		return fmt.Errorf("firecracker runtime: snapshots require rootfs_mode=readonly until a block-level CoW backend is available")
	}
	if net.ParseIP(c.GuestIP) == nil {
		return fmt.Errorf("firecracker runtime: invalid guest IP %q", c.GuestIP)
	}
	if net.ParseIP(c.GatewayIP) == nil {
		return fmt.Errorf("firecracker runtime: invalid gateway IP %q", c.GatewayIP)
	}
	if net.ParseIP(c.MMDSAddress) == nil {
		return fmt.Errorf("firecracker runtime: invalid MMDS address %q", c.MMDSAddress)
	}
	if _, _, err := net.ParseCIDR(c.InternalCIDR); err != nil {
		return fmt.Errorf("firecracker runtime: internal CIDR %q: %w", c.InternalCIDR, err)
	}
	if _, _, err := net.ParseCIDR(c.ExposedCIDR); err != nil {
		return fmt.Errorf("firecracker runtime: exposed CIDR %q: %w", c.ExposedCIDR, err)
	}
	if c.ProxyAdvertiseAddress != "" && net.ParseIP(c.ProxyAdvertiseAddress) == nil {
		return fmt.Errorf("firecracker runtime: invalid proxy advertise address %q", c.ProxyAdvertiseAddress)
	}
	if err := os.MkdirAll(filepath.Join(c.WorkDir, "instances"), 0o755); err != nil {
		return fmt.Errorf("firecracker runtime: create work dir: %w", err)
	}
	if c.UseSnapshots {
		if err := os.MkdirAll(c.SnapshotDir, 0o755); err != nil {
			return fmt.Errorf("firecracker runtime: create snapshot dir: %w", err)
		}
	}
	return nil
}
