package leaf

import (
	"fmt"
	"time"

	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

// WorkerEndpoint is a worker node managed by this leaf.
type WorkerEndpoint struct {
	// Address is the worker gRPC API address used for sandbox lifecycle RPCs.
	Address string `yaml:"address"`
}

func (e WorkerEndpoint) Validate() error {
	if e.Address == "" {
		return fmt.Errorf("worker address is required")
	}
	return nil
}

// DataplaneConfig configures leaf-local invoke, queueing, and worker connectivity.
type DataplaneConfig struct {
	// ScaleToZeroAfter is the default idle time before a function scales to zero when the function does not override it.
	ScaleToZeroAfter time.Duration `yaml:"scale_to_zero_after"`
	// MaxInstancesPerWorker caps how many sandboxes this leaf may place on one worker.
	// Zero means unlimited (no per-worker instance cap).
	MaxInstancesPerWorker uint32 `yaml:"max_instances_per_worker"`
	// StartTokensPerWorker caps concurrent sandbox starts per worker.
	// Zero uses the actuator's safe default of 8 concurrent starts per worker. Warm request serving is not affected.
	StartTokensPerWorker uint32 `yaml:"start_tokens_per_worker"`
	// MaxStartsPerReconcile caps concurrent sandbox starts admitted by this leaf.
	// Zero derives workers * start_tokens_per_worker, or workers * 8 when start tokens are zero.
	MaxStartsPerReconcile uint32 `yaml:"max_starts_per_reconcile"`
	// DirigentStrictAdmission enables Dirigent-copy admission: scale-from-zero starts one replica;
	// subsequent decisions count pending starts as scale and dispatch the full desired gap without
	// HyperFaaS start-token limits. Placement and worker instance-cap checks still apply.
	DirigentStrictAdmission bool `yaml:"dirigent_strict_admission"`
	// DialTimeout bounds control/data-plane dials made by the leaf.
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// StartTimeout bounds worker sandbox startup, including image preparation and readiness.
	StartTimeout time.Duration `yaml:"start_timeout"`
	// StopTimeout bounds worker sandbox shutdown.
	StopTimeout time.Duration `yaml:"stop_timeout"`
	// ScaleDownDelay delays any instance stop after a downscale decision.
	// Drain and StopSandbox still use StopTimeout after this delay.
	ScaleDownDelay time.Duration `yaml:"scale_down_delay"`
	// StatusBackoff is the retry delay for control-plane and leaf-state watch reconnects.
	StatusBackoff time.Duration `yaml:"status_backoff"`
	// RoutingStateHeartbeatInterval is the bootstrap fallback for the
	// routing-state heartbeat cadence. The dynamic
	// PlatformConfig.state_refresh_interval wins once the control-plane document
	// arrives.
	RoutingStateHeartbeatInterval time.Duration `yaml:"routing_state_heartbeat_interval"`
	// HTTPMaxIdleConns caps idle HTTP connections kept by the leaf function invoker across all function instances.
	HTTPMaxIdleConns int `yaml:"http_max_idle_conns"`
	// HTTPMaxIdleConnsPerHost caps idle HTTP connections kept per function instance address.
	HTTPMaxIdleConnsPerHost int `yaml:"http_max_idle_conns_per_host"`
	// HTTPMaxConnsPerHost caps total active plus idle HTTP connections per sandbox. Zero selects the finite platform default.
	HTTPMaxConnsPerHost int `yaml:"http_max_conns_per_host"`
	// HTTPIdleConnTimeout closes idle leaf-to-function HTTP connections after this duration.
	HTTPIdleConnTimeout time.Duration `yaml:"http_idle_conn_timeout"`
	// Containerized indicates the leaf itself runs in a containerized deployment and may need container-network address handling.
	Containerized bool `yaml:"containerized"`
}

func (c DataplaneConfig) Validate() error {
	if c.ScaleToZeroAfter <= 0 {
		return fmt.Errorf("dataplane.scale_to_zero_after is required")
	}
	if c.DialTimeout <= 0 {
		return fmt.Errorf("dataplane.dial_timeout is required")
	}
	if c.StartTimeout <= 0 {
		return fmt.Errorf("dataplane.start_timeout is required")
	}
	if c.StopTimeout <= 0 {
		return fmt.Errorf("dataplane.stop_timeout is required")
	}
	if c.ScaleDownDelay < 0 {
		return fmt.Errorf("dataplane.scale_down_delay must be >= 0")
	}
	if c.StatusBackoff <= 0 {
		return fmt.Errorf("dataplane.status_backoff is required")
	}
	if c.RoutingStateHeartbeatInterval <= 0 {
		return fmt.Errorf("dataplane.routing_state_heartbeat_interval is required")
	}
	if c.HTTPMaxIdleConns <= 0 {
		return fmt.Errorf("dataplane.http_max_idle_conns is required")
	}
	if c.HTTPMaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("dataplane.http_max_idle_conns_per_host is required")
	}
	if c.HTTPMaxConnsPerHost < 0 {
		return fmt.Errorf("dataplane.http_max_conns_per_host must be >= 0")
	}
	if c.HTTPIdleConnTimeout <= 0 {
		return fmt.Errorf("dataplane.http_idle_conn_timeout is required")
	}
	return nil
}

// InstancesPerWorkerUnlimited reports whether max_instances_per_worker disables the cap.
func InstancesPerWorkerUnlimited(max uint32) bool {
	return max == 0
}

// InstancesPerWorkerAtCap reports whether count has reached the per-worker sandbox cap.
func InstancesPerWorkerAtCap(count int, max uint32) bool {
	if InstancesPerWorkerUnlimited(max) {
		return false
	}
	return count >= int(max)
}

// AutoscalingConfig configures the leaf-local autoscaler. The autoscaling
// strategy is fixed in code; only these knobs are configurable.
type AutoscalingConfig struct {
	// ReconcileInterval is how often the autoscaler samples local signals and applies scale decisions.
	ReconcileInterval time.Duration `yaml:"reconcile_interval"`
	// UnlimitedConcurrencyTarget is the soft in-flight target per sandbox when
	// max_concurrency=0 and the function does not set target_concurrency.
	// Knative's container-concurrency-target-default is 100.
	UnlimitedConcurrencyTarget uint64 `yaml:"unlimited_concurrency_target"`
	// TargetUtilization is Knative's container-concurrency-target-percentage.
	// 0 defaults to 1.0 (no headroom, cold-storm with max_concurrency=1 unchanged).
	// Values in (1, 100] are percentages (70 means 0.7).
	TargetUtilization float64 `yaml:"target_utilization"`
	// ColdStartPanicScaling uses instantaneous in-flight demand for low-concurrency cold storms.
	ColdStartPanicScaling bool `yaml:"cold_start_panic_scaling"`
	// StableWindow is the moving average window for normal scaling decisions.
	// Zero preserves the current 6 second default.
	StableWindow time.Duration `yaml:"stable_window"`
	// PanicWindow is the moving average window used to enter panic mode.
	// Zero preserves the current 2 second default.
	PanicWindow time.Duration `yaml:"panic_window"`
	// PanicThresholdRatio is the panic desired/current scale ratio.
	// Zero preserves the current 2.0 default.
	PanicThresholdRatio float64 `yaml:"panic_threshold_ratio"`
	// MaxScaleUpRate limits scale-up to this multiple of current scale.
	// Zero preserves the current 1000.0 default.
	MaxScaleUpRate float64 `yaml:"max_scale_up_rate"`
	// MaxScaleDownRate limits scale-down to at most 1/rate of current scale.
	// Zero preserves the current 2.0 default.
	MaxScaleDownRate float64 `yaml:"max_scale_down_rate"`
}

func (c AutoscalingConfig) Validate() error {
	if c.ReconcileInterval <= 0 {
		return fmt.Errorf("autoscaling.reconcile_interval is required")
	}
	if c.UnlimitedConcurrencyTarget == 0 {
		return fmt.Errorf("autoscaling.unlimited_concurrency_target is required")
	}
	if c.TargetUtilization < 0 || c.TargetUtilization > 100 {
		return fmt.Errorf("autoscaling.target_utilization must be in [0, 100]")
	}
	if c.StableWindow < 0 || c.PanicWindow < 0 {
		return fmt.Errorf("autoscaling windows must be >= 0")
	}
	if c.PanicThresholdRatio < 0 || c.MaxScaleUpRate < 0 || c.MaxScaleDownRate < 0 {
		return fmt.Errorf("autoscaling ratios and rates must be >= 0")
	}
	return nil
}

// GRPCProxyConfig configures the optional transparent gRPC proxy listener.
type GRPCProxyConfig struct {
	ListenAddress string `yaml:"listen_address"`
}

func (c GRPCProxyConfig) Validate() error {
	return nil
}

// LeafConfig is the YAML configuration for a leaf node.
type LeafConfig struct {
	LeafID       uint64                    `yaml:"leaf_id"`
	NodeID       string                    `yaml:"node_id"`
	Logging      config.LoggingConfig      `yaml:"logging"`
	Server       config.ServerConfig       `yaml:"server"`
	GRPCProxy    GRPCProxyConfig           `yaml:"grpc_proxy"`
	Workers      []WorkerEndpoint          `yaml:"workers"`
	Dataplane    DataplaneConfig           `yaml:"dataplane"`
	Autoscaling  AutoscalingConfig         `yaml:"autoscaling"`
	ControlPlane config.ControlPlaneConfig `yaml:"controlplane"`
	// HTTPInvocationAddress is the internal streaming HTTP invocation listen
	// address used by ingress. Empty disables the streaming HTTP listener.
	HTTPInvocationAddress string `yaml:"http_invocation_address"`
}

// LoadConfig reads and validates a leaf YAML config file.
func LoadConfig(path string) (LeafConfig, error) {
	var cfg LeafConfig
	if err := utils.LoadYAML(path, &cfg); err != nil {
		return LeafConfig{}, err
	}
	return cfg, cfg.Validate()
}

func (c LeafConfig) Validate() error {
	if c.LeafID == 0 {
		return fmt.Errorf("leaf: leaf_id is required")
	}
	if err := c.GRPCProxy.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	if len(c.Workers) == 0 {
		return fmt.Errorf("leaf: at least one worker is required")
	}
	for i, worker := range c.Workers {
		if err := worker.Validate(); err != nil {
			return fmt.Errorf("leaf: workers[%d]: %w", i, err)
		}
	}
	if err := c.Dataplane.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	if err := c.Autoscaling.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	if err := c.ControlPlane.Validate(); err != nil {
		return fmt.Errorf("leaf: %w", err)
	}
	return nil
}

// WorkerAddresses returns the configured worker gRPC addresses.
func (c LeafConfig) WorkerAddresses() []string {
	addrs := make([]string, len(c.Workers))
	for i, w := range c.Workers {
		addrs[i] = w.Address
	}
	return addrs
}
