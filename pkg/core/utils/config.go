package utils

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"hyperfaas-ideal-arch/pkg/core/config"
)

const configEnvVar = "HYPERFAAS_CONFIG"

// LoadYAML reads path and unmarshals into dst.
func LoadYAML(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	return nil
}

// ConfigPath resolves the YAML config file path from -config and HYPERFAAS_CONFIG.
func ConfigPath(defaultPath string) string {
	path := os.Getenv(configEnvVar)
	if path == "" {
		path = defaultPath
	}
	flag.StringVar(&path, "config", path, "path to YAML config file")
	flag.Parse()
	return path
}

// SetupLogger builds a slog.Logger from a validated LoggingConfig.
func SetupLogger(cfg config.LoggingConfig) *slog.Logger {
	var writer io.Writer = os.Stdout
	if cfg.File != "" {
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Error("failed to open log file, falling back to stdout", "path", cfg.File, "error", err)
		} else {
			writer = f
		}
	}

	level := parseLogLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	return slog.New(handler)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
