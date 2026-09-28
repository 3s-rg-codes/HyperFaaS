package worker

import (
	"fmt"
	"time"

	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

type DockerConfig struct {
	AutoRemove  bool   `yaml:"auto_remove"`
	NetworkName string `yaml:"network_name"`
}

type FakeConfig struct {
	SimulateSandboxStartLatency bool `yaml:"simulate_sandbox_start_latency"`
}

type FirecrackerConfig struct {
	FirecrackerBin  string `yaml:"firecracker_bin"`
	KernelImagePath string `yaml:"kernel_image_path"`
	RootfsImagePath string `yaml:"rootfs_image_path"`
	InitrdImagePath string `yaml:"initrd_image_path"`
	KernelArgs      string `yaml:"kernel_args"`
	WorkDir         string `yaml:"work_dir"`
	SnapshotDir     string `yaml:"snapshot_dir"`
	UseSnapshots    bool   `yaml:"use_snapshots"`
	Debug           bool   `yaml:"debug"`
	RootfsMode      string `yaml:"rootfs_mode"`
	GuestIP         string `yaml:"guest_ip"`
	GatewayIP       string `yaml:"gateway_ip"`
	GuestMAC        string `yaml:"guest_mac"`
	MMDSAddress     string `yaml:"mmds_address"`
	InternalCIDR    string `yaml:"internal_cidr"`
	ExposedCIDR     string `yaml:"exposed_cidr"`
	// ProxyAdvertiseAddress is the worker-reachable IP returned for sandbox port proxies.
	// It is optional when server.listen_address binds a specific IP.
	ProxyAdvertiseAddress string `yaml:"proxy_advertise_address"`

	UsePool       bool `yaml:"use_pool"`
	PoolSize      int  `yaml:"pool_size"`
	AsyncTeardown bool `yaml:"async_teardown"`
}

type RunCConfig struct {
	WorkDir          string `yaml:"work_dir"`
	NetworkIsolation bool   `yaml:"network_isolation"`
	NetworkMode      string `yaml:"network_mode"` // veth, ipvlan
	UsePool          bool   `yaml:"use_pool"`
	PoolSize         int    `yaml:"pool_size"`
}

type ContainerdConfig struct {
	CRIPath       string `yaml:"cri_path"`
	CNIConfigPath string `yaml:"cni_config_path"`
	Namespace     string `yaml:"namespace"`
	PrefetchImage bool   `yaml:"prefetch_image"`

	// UsePool enables network namespace pooling (Veth Pooling) to bypass CNI setup serialization.
	// Resolves kernel mount/namespace lock contention during concurrent cold starts.
	UsePool  bool `yaml:"use_pool"`
	PoolSize int  `yaml:"pool_size"`

	// AsyncTeardown defers CNI cleanup and containerd resource deletion to a background goroutine.
	// Resolves synchronous blocking in container stop operations.
	AsyncTeardown bool `yaml:"async_teardown"`

	// SingleFlightPull deduplicates concurrent image pull requests for the same image.
	// Resolves network redundancy and registry rate-limiting during cold storms.
	SingleFlightPull bool `yaml:"single_flight_pull"`
}

// RuntimeConfig selects the sandbox isolation backend.
type RuntimeConfig struct {
	Type        string            `yaml:"type"` // docker, firecracker, runc, containerd, or fake
	Docker      DockerConfig      `yaml:"docker"`
	Firecracker FirecrackerConfig `yaml:"firecracker"`
	RunC        RunCConfig        `yaml:"runc"`
	Containerd  ContainerdConfig  `yaml:"containerd"`
	Fake        FakeConfig        `yaml:"fake"`
}

func (c RuntimeConfig) Validate() error {
	switch c.Type {
	case "docker":
		if c.Docker.NetworkName == "" {
			return fmt.Errorf("runtime.docker.network_name is required")
		}
	case "firecracker":
		if c.Firecracker.KernelImagePath == "" {
			return fmt.Errorf("runtime.firecracker.kernel_image_path is required")
		}
		if c.Firecracker.RootfsImagePath == "" && c.Firecracker.InitrdImagePath == "" {
			return fmt.Errorf("runtime.firecracker.rootfs_image_path or runtime.firecracker.initrd_image_path is required")
		}
		if c.Firecracker.RootfsMode != "" && c.Firecracker.RootfsMode != "copy" && c.Firecracker.RootfsMode != "readonly" {
			return fmt.Errorf("runtime.firecracker.rootfs_mode must be copy or readonly")
		}
		if c.Firecracker.UseSnapshots && c.Firecracker.InitrdImagePath == "" && c.Firecracker.RootfsMode != "readonly" {
			return fmt.Errorf("runtime.firecracker.use_snapshots requires runtime.firecracker.rootfs_mode=readonly")
		}
	case "runc":
		if c.RunC.WorkDir == "" {
			return fmt.Errorf("runtime.runc.work_dir is required")
		}
	case "containerd":
		if c.Containerd.CRIPath == "" {
			return fmt.Errorf("runtime.containerd.cri_path is required")
		}
	case "fake":
		// In-process echo/sleep sandboxes; no extra knobs required.
	default:
		return fmt.Errorf("runtime.type must be docker, firecracker, runc, containerd or fake, got %q", c.Type)
	}
	return nil
}

// StatsConfig configures worker resource sampling.
type StatsConfig struct {
	UpdateBufferSize int64         `yaml:"update_buffer_size"`
	MetricsInterval  time.Duration `yaml:"metrics_interval"`
	BudgetCPU        float64       `yaml:"budget_cpu"`
	BudgetMemory     int64         `yaml:"budget_memory"`
}

func (c StatsConfig) Validate() error {
	if c.UpdateBufferSize <= 0 {
		return fmt.Errorf("stats.update_buffer_size is required")
	}
	if c.MetricsInterval <= 0 {
		return fmt.Errorf("stats.metrics_interval is required")
	}
	if c.BudgetCPU <= 0 {
		return fmt.Errorf("stats.budget_cpu is required")
	}
	if c.BudgetMemory <= 0 {
		return fmt.Errorf("stats.budget_memory is required")
	}
	return nil
}

// WorkerConfig is the YAML configuration for a worker node.
type WorkerConfig struct {
	NodeID          string               `yaml:"node_id"`
	Logging         config.LoggingConfig `yaml:"logging"`
	Server          config.ServerConfig  `yaml:"server"`
	Runtime         RuntimeConfig        `yaml:"runtime"`
	Stats           StatsConfig          `yaml:"stats"`
	ArtifactsBucket string               `yaml:"artifacts_bucket"`
}

// LoadConfig reads and validates a worker YAML config file.
func LoadConfig(path string) (WorkerConfig, error) {
	var cfg WorkerConfig
	if err := utils.LoadYAML(path, &cfg); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, cfg.Validate()
}

func (c WorkerConfig) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("worker: node_id is required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := c.Runtime.Validate(); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := c.Stats.Validate(); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	return nil
}
