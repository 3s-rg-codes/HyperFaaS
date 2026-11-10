//go:build linux

package worker

import (
	"log/slog"

	"hyperfaas-ideal-arch/pkg/worker/runtime"
	firecrackerruntime "hyperfaas-ideal-arch/pkg/worker/runtime/firecracker"
	runcruntime "hyperfaas-ideal-arch/pkg/worker/runtime/runc"
)

func newFirecrackerRuntime(cfg WorkerConfig, logger *slog.Logger) (runtime.Runtime, error) {
	return firecrackerruntime.New(firecrackerruntime.Config{
		FirecrackerBin:        cfg.Runtime.Firecracker.FirecrackerBin,
		KernelImagePath:       cfg.Runtime.Firecracker.KernelImagePath,
		RootfsImagePath:       cfg.Runtime.Firecracker.RootfsImagePath,
		InitrdImagePath:       cfg.Runtime.Firecracker.InitrdImagePath,
		KernelArgs:            cfg.Runtime.Firecracker.KernelArgs,
		WorkDir:               cfg.Runtime.Firecracker.WorkDir,
		SnapshotDir:           cfg.Runtime.Firecracker.SnapshotDir,
		UseSnapshots:          cfg.Runtime.Firecracker.UseSnapshots,
		Debug:                 cfg.Runtime.Firecracker.Debug,
		RootfsMode:            cfg.Runtime.Firecracker.RootfsMode,
		GuestIP:               cfg.Runtime.Firecracker.GuestIP,
		GatewayIP:             cfg.Runtime.Firecracker.GatewayIP,
		GuestMAC:              cfg.Runtime.Firecracker.GuestMAC,
		MMDSAddress:           cfg.Runtime.Firecracker.MMDSAddress,
		InternalCIDR:          cfg.Runtime.Firecracker.InternalCIDR,
		ExposedCIDR:           cfg.Runtime.Firecracker.ExposedCIDR,
		UsePool:               cfg.Runtime.Firecracker.UsePool,
		PoolSize:              cfg.Runtime.Firecracker.PoolSize,
		AsyncTeardown:         cfg.Runtime.Firecracker.AsyncTeardown,
		WorkerListenAddress:   cfg.Server.ListenAddress,
		ProxyAdvertiseAddress: cfg.Runtime.Firecracker.ProxyAdvertiseAddress,
		ArtifactsBucket:       cfg.ArtifactsBucket,
	}, logger)
}

func newRuncRuntime(cfg WorkerConfig, logger *slog.Logger) (runtime.Runtime, error) {
	return runcruntime.New(runcruntime.Config{
		WorkDir:             cfg.Runtime.RunC.WorkDir,
		WorkerListenAddress: cfg.Server.ListenAddress,
		NetworkIsolation:    cfg.Runtime.RunC.NetworkIsolation,
		NetworkMode:         cfg.Runtime.RunC.NetworkMode,
		UsePool:             cfg.Runtime.RunC.UsePool,
		PoolSize:            cfg.Runtime.RunC.PoolSize,
		ArtifactsBucket:     cfg.ArtifactsBucket,
	}, logger)
}
