package ingress

import (
	"context"
	"log/slog"

	"hyperfaas-ideal-arch/pkg/controlplaneclient"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/ingress/routing"
)

// Runtime holds wired ingress dependencies.
//
// The routing policy is not held here: the RoutingController owns it and drives
// the Engine the request path uses.
type Runtime struct {
	Config     IngressConfig
	Engine     *routing.Engine
	Controller *routing.RoutingController
	// ConfigSync streams dynamic policy changes from the control plane. It is
	// nil only in tests that do not exercise config reload.
	ConfigSync *controlplaneclient.ConfigSubscriber
	Logger     *slog.Logger

	ctx context.Context
}

// Bootstrap wires the routing controller and prepares the control-plane
// configuration watch. Routing is unavailable until the watch delivers the
// first policy document; there is no static YAML policy.
func Bootstrap(ctx context.Context, cfg IngressConfig, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}

	addresses := make(map[uint64]routing.LeafAddress, len(cfg.Leaves))
	for _, leaf := range cfg.Leaves {
		addresses[leaf.ID] = routing.LeafAddress{
			HTTPAddress:    leaf.httpInvocationAddr(),
			ControlAddress: leaf.invocationAddr(),
		}
	}

	controller := routing.NewRoutingController(routing.ControllerConfig{
		Topology:    routing.NewTopology(addresses),
		DialTimeout: cfg.ControlPlane.DialTimeout,
		Backoff:     cfg.Routing.StateSyncInterval,
		Logger:      logger,
	})

	// Routing is unavailable until the control-plane config document arrives.
	// The control plane seeds a default document on startup, so the watch
	// delivers the first policy without any static YAML fallback. This is the
	// intended behavior: there is no policy selection in YAML and no fallback to
	// a previous policy.
	rt := &Runtime{
		Config:     cfg,
		Engine:     controller.Engine(),
		Controller: controller,
		Logger:     logger,
		ctx:        ctx,
	}
	if cfg.ControlPlane.Address != "" {
		rt.ConfigSync = controlplaneclient.NewConfigSubscriber(
			cfg.ControlPlane.Address,
			cfg.ControlPlane.DialTimeout,
			cfg.Routing.StateSyncInterval,
			logger,
			rt.ApplyPlatformConfig,
		)
	}
	return rt, nil
}

// ApplyPlatformConfig reloads the routing policy from a dynamic platform-config
// document. The controller clears routing, restarts the leaf streams with the
// new policy's projection, and installs the new picker once baselines arrive.
func (rt *Runtime) ApplyPlatformConfig(cfg *core.PlatformConfig) {
	if cfg == nil || cfg.GetRouting() == nil {
		return
	}
	if err := rt.Controller.ApplyConfig(rt.ctx, cfg.GetRouting(), cfg.GetVersion()); err != nil {
		rt.Logger.Warn("failed to apply routing policy",
			"policy", routing.RoutingPolicyName(cfg.GetRouting()),
			"config_version", cfg.GetVersion(),
			"error", err,
		)
		return
	}
	rt.Logger.Info("applied routing policy",
		"policy", routing.RoutingPolicyName(cfg.GetRouting()),
		"config_version", cfg.GetVersion(),
	)
}

// StartWatchers runs the dynamic platform-config watch. Leaf routing-state
// streams are owned by the routing controller.
func (rt *Runtime) StartWatchers(ctx context.Context) {
	if rt.ConfigSync != nil {
		go rt.ConfigSync.Run(ctx)
	}
}
