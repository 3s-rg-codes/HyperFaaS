package leaf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoscalingConfigRejectsBadUtilization(t *testing.T) {
	cfg := AutoscalingConfig{
		ReconcileInterval:          time.Second,
		UnlimitedConcurrencyTarget: 100,
		TargetUtilization:          101,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected utilization > 100 to fail")
	}
}

func TestAutoscalingConfigAllowsUnsetUtilization(t *testing.T) {
	cfg := AutoscalingConfig{
		ReconcileInterval:          time.Second,
		UnlimitedConcurrencyTarget: 100,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigAcceptsAutoscalingWindowsAndScaleDownDelay(t *testing.T) {
	cfg := AutoscalingConfig{
		ReconcileInterval:          time.Second,
		UnlimitedConcurrencyTarget: 100,
		StableWindow:               time.Minute,
		PanicWindow:                6 * time.Second,
		PanicThresholdRatio:        2,
		MaxScaleUpRate:             1000,
		MaxScaleDownRate:           2,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (DataplaneConfig{ScaleDownDelay: -time.Second}).Validate(); err == nil {
		t.Fatal("expected negative scale-down delay to fail")
	}
}

func TestLoadConfigAcceptsKnativeSoftTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leaf.yaml")
	body := `leaf_id: 1
node_id: leaf-1
logging:
  level: info
  format: text
server:
  listen_address: 127.0.0.1:1
workers:
  - address: 127.0.0.1:2
dataplane:
  scale_to_zero_after: 10s
  scale_down_delay: 10s
  dial_timeout: 1s
  start_timeout: 1s
  stop_timeout: 1s
  status_backoff: 1s
  routing_state_heartbeat_interval: 1s
  http_max_idle_conns: 1
  http_max_idle_conns_per_host: 1
  http_idle_conn_timeout: 1s
autoscaling:
  reconcile_interval: 2s
  stable_window: 60s
  panic_window: 6s
  panic_threshold_ratio: 2.0
  max_scale_up_rate: 1000.0
  max_scale_down_rate: 2.0
  unlimited_concurrency_target: 100
  target_utilization: 0.7
controlplane:
  address: 127.0.0.1:3
  dial_timeout: 1s
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Autoscaling.UnlimitedConcurrencyTarget != 100 {
		t.Fatalf("default total=%d", cfg.Autoscaling.UnlimitedConcurrencyTarget)
	}
	if cfg.Autoscaling.TargetUtilization != 0.7 {
		t.Fatalf("utilization=%v", cfg.Autoscaling.TargetUtilization)
	}
	if cfg.Dataplane.ScaleDownDelay != 10*time.Second {
		t.Fatalf("scale-down delay=%s", cfg.Dataplane.ScaleDownDelay)
	}
	if cfg.Autoscaling.StableWindow != time.Minute || cfg.Autoscaling.PanicWindow != 6*time.Second {
		t.Fatalf("windows=%s/%s", cfg.Autoscaling.StableWindow, cfg.Autoscaling.PanicWindow)
	}
}
