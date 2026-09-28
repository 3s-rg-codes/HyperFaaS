package config

import (
	"fmt"
	"strings"
	"time"
)

// LoggingConfig configures structured logging via slog.
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // text, json
	File   string `yaml:"file"`   // empty = stdout
}

func (c LoggingConfig) Validate() error {
	if c.Level == "" {
		return fmt.Errorf("logging.level is required")
	}
	switch strings.ToLower(c.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error, got %q", c.Level)
	}
	if c.Format == "" {
		return fmt.Errorf("logging.format is required")
	}
	switch strings.ToLower(c.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("logging.format must be text or json, got %q", c.Format)
	}
	return nil
}

// ServerConfig configures the gRPC listen address and optional pprof endpoint.
type ServerConfig struct {
	ListenAddress string `yaml:"listen_address"`
	PprofAddress  string `yaml:"pprof_address"`
}

func (c ServerConfig) Validate() error {
	if c.ListenAddress == "" {
		return fmt.Errorf("server.listen_address is required")
	}
	return nil
}

// ControlPlaneConfig configures connectivity to the control plane API.
type ControlPlaneConfig struct {
	Address     string        `yaml:"address"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
}

func (c ControlPlaneConfig) Validate() error {
	if c.Address == "" {
		return fmt.Errorf("controlplane.address is required")
	}
	if c.DialTimeout <= 0 {
		return fmt.Errorf("controlplane.dial_timeout is required")
	}
	return nil
}
