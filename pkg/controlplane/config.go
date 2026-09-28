package controlplane

import (
	"fmt"
	"time"

	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

// StoreConfig selects the metadata backend.
type StoreConfig struct {
	Type        string        `yaml:"type"` // etcd
	Endpoints   []string      `yaml:"endpoints"`
	Prefix      string        `yaml:"prefix"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
}

func (c StoreConfig) Validate() error {
	if c.Type != "etcd" {
		return fmt.Errorf("store.type must be etcd, got %q", c.Type)
	}
	if len(c.Endpoints) == 0 {
		return fmt.Errorf("store.endpoints is required")
	}
	if c.Prefix == "" {
		return fmt.Errorf("store.prefix is required")
	}
	if c.DialTimeout <= 0 {
		return fmt.Errorf("store.dial_timeout is required")
	}
	return nil
}

// ServerConfig configures the control plane HTTP and gRPC listeners.
type ServerConfig struct {
	HTTPAddress  string `yaml:"http_address"`
	GRPCAddress  string `yaml:"grpc_address"`
	PprofAddress string `yaml:"pprof_address"`
}

func (c ServerConfig) Validate() error {
	if c.HTTPAddress == "" {
		return fmt.Errorf("server.http_address is required")
	}
	if c.GRPCAddress == "" {
		return fmt.Errorf("server.grpc_address is required")
	}
	if c.HTTPAddress == c.GRPCAddress {
		return fmt.Errorf("server.http_address and server.grpc_address must differ")
	}
	return nil
}

// ControlPlaneConfig is the YAML configuration for a control plane node.
type ControlPlaneConfig struct {
	NodeID  string               `yaml:"node_id"`
	Logging config.LoggingConfig `yaml:"logging"`
	Server  ServerConfig         `yaml:"server"`
	Store   StoreConfig          `yaml:"store"`
}

func LoadConfig(path string) (ControlPlaneConfig, error) {
	var cfg ControlPlaneConfig
	if err := utils.LoadYAML(path, &cfg); err != nil {
		return ControlPlaneConfig{}, err
	}
	return cfg, cfg.Validate()
}

func (c ControlPlaneConfig) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("controlplane: node_id is required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}
	if err := c.Store.Validate(); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}
	return nil
}
