package ingress

import (
	"fmt"
	"time"

	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

// ServerConfig configures the ingress HTTP and transparent gRPC proxy listen addresses.
type ServerConfig struct {
	HTTPAddress      string `yaml:"http_address"`
	GRPCProxyAddress string `yaml:"grpc_proxy_address"`
	PprofAddress     string `yaml:"pprof_address"`
}

func (c ServerConfig) Validate() error {
	if c.HTTPAddress == "" {
		return fmt.Errorf("server.http_address is required")
	}
	if c.GRPCProxyAddress == "" {
		return fmt.Errorf("server.grpc_proxy_address is required")
	}
	addrs := []struct {
		name string
		addr string
	}{
		{"server.http_address", c.HTTPAddress},
		{"server.grpc_proxy_address", c.GRPCProxyAddress},
	}
	for i := range addrs {
		for j := i + 1; j < len(addrs); j++ {
			if addrs[i].addr == addrs[j].addr {
				return fmt.Errorf("%s and %s must differ (both %q)", addrs[i].name, addrs[j].name, addrs[i].addr)
			}
		}
	}
	return nil
}

// LeafEndpoint is a known leaf node reachable from this ingress.
type LeafEndpoint struct {
	// ID is the stable leaf identifier used in routing decisions.
	ID uint64 `yaml:"id"`
	// InvocationAddress is the leaf gRPC address for ingress-to-leaf invoke RPCs and state watch RPCs.
	InvocationAddress string `yaml:"invocation_address"`
	// HTTPInvocationAddress is the leaf's internal streaming HTTP invocation address.
	// It is used by the streaming HTTP proxy path.
	HTTPInvocationAddress string `yaml:"http_invocation_address"`
	// GRPCProxyAddress is the leaf transparent gRPC proxy address used for direct gRPC function proxying.
	GRPCProxyAddress string `yaml:"grpc_proxy_address"`
	// Address is a legacy alias for invocation_address.
	Address string `yaml:"address"`
}

func (e LeafEndpoint) invocationAddr() string {
	if e.InvocationAddress != "" {
		return e.InvocationAddress
	}
	return e.Address
}

func (e LeafEndpoint) httpInvocationAddr() string {
	return e.HTTPInvocationAddress
}

func (e LeafEndpoint) Validate() error {
	if e.ID == 0 {
		return fmt.Errorf("leaf id is required")
	}
	if e.invocationAddr() == "" {
		return fmt.Errorf("invocation_address is required")
	}
	if e.GRPCProxyAddress == "" {
		return fmt.Errorf("grpc_proxy_address is required")
	}
	return nil
}

// RoutingConfig configures the ingress routing transport and the reconnect
// backoff for leaf state watches. The routing policy itself is not here: it
// comes from the control-plane PlatformConfig document, so it can change while
// the cluster is running.
type RoutingConfig struct {
	// LeafTransport selects the ingress-to-leaf invocation transport.
	// Empty selects h2c, the production default. Other values are experiment
	// arms for comparing protocol/connection models.
	LeafTransport string `yaml:"leaf_transport"`
	// StateSyncInterval is the reconnect backoff after a leaf state watch fails.
	StateSyncInterval time.Duration `yaml:"state_sync_interval"`
}

func (c RoutingConfig) Validate() error {
	switch c.LeafTransport {
	case "", LeafTransportH2C, LeafTransportHTTP1, LeafTransportHTTP1NoKeepAlive:
	default:
		return fmt.Errorf("routing.leaf_transport must be %s, %s, or %s", LeafTransportH2C, LeafTransportHTTP1, LeafTransportHTTP1NoKeepAlive)
	}
	if c.StateSyncInterval <= 0 {
		return fmt.Errorf("routing.state_sync_interval is required")
	}
	return nil
}

// IngressConfig is the YAML configuration for an ingress node.
type IngressConfig struct {
	NodeID       string                    `yaml:"node_id"`
	Logging      config.LoggingConfig      `yaml:"logging"`
	Server       ServerConfig              `yaml:"server"`
	ControlPlane config.ControlPlaneConfig `yaml:"controlplane"`
	Leaves       []LeafEndpoint            `yaml:"leaves"`
	Routing      RoutingConfig             `yaml:"routing"`
}

// LoadConfig reads and validates an ingress YAML config file.
func LoadConfig(path string) (IngressConfig, error) {
	var cfg IngressConfig
	if err := utils.LoadYAML(path, &cfg); err != nil {
		return IngressConfig{}, err
	}
	return cfg, cfg.Validate()
}

func (c IngressConfig) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("ingress: node_id is required")
	}
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	if err := c.ControlPlane.Validate(); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	if len(c.Leaves) == 0 {
		return fmt.Errorf("ingress: at least one leaf is required")
	}
	seenIDs := make(map[uint64]int, len(c.Leaves))
	for i, leaf := range c.Leaves {
		if err := leaf.Validate(); err != nil {
			return fmt.Errorf("ingress: leaves[%d]: %w", i, err)
		}
		if prev, dup := seenIDs[leaf.ID]; dup {
			return fmt.Errorf("ingress: leaves[%d].id and leaves[%d].id must be unique (both %d)", prev, i, leaf.ID)
		}
		seenIDs[leaf.ID] = i
	}
	if err := c.Routing.Validate(); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	return nil
}
