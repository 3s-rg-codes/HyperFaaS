package firecracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	fc "github.com/firecracker-microvm/firecracker-go-sdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
)

// Runtime manages HyperFaaS function sandboxes as one Firecracker microVM per
// instance. It is intentionally a direct microVM runtime: the function image is
// interpreted as a raw rootfs path unless RootfsImagePath is configured. OCI
// image unpacking belongs in the future firecracker-containerd runtime.
type Runtime struct {
	cfg       Config
	logger    *slog.Logger
	networks  *networkManager
	snapshots *snapshotManager

	mu        sync.RWMutex
	instances map[uint64]*vmInstance
	starting  map[uint64]struct{}
	reaping   map[uint64]struct{}

	teardownMu      sync.Mutex
	teardownCond    *sync.Cond
	activeTeardowns int
}

const failedMachineCleanupTimeout = 10 * time.Second

// vmmGracefulExitTimeout bounds how long a clean shutdown is given before the
// VMM is force-killed during teardown.
const vmmGracefulExitTimeout = 3 * time.Second

func New(cfg Config, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		return nil, fmt.Errorf("firecracker runtime: logger is required")
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	networks, err := newNetworkManager(
		cfg.InternalCIDR,
		cfg.ExposedCIDR,
		cfg.UsePool,
		cfg.PoolSize,
		filepath.Join(cfg.WorkDir, "netns"),
		net.ParseIP(cfg.GuestIP),
		net.ParseIP(cfg.GatewayIP),
		cfg.GuestMAC,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("firecracker runtime: create network manager: %w", err)
	}
	if !firecrackerAvailable(cfg.FirecrackerBin) {
		logger.Warn("firecracker binary was not found in PATH", "binary", cfg.FirecrackerBin)
	}
	rt := &Runtime{
		cfg:       cfg,
		logger:    logger,
		networks:  networks,
		snapshots: newSnapshotManager(cfg.SnapshotDir),
		instances: make(map[uint64]*vmInstance),
		starting:  make(map[uint64]struct{}),
		reaping:   make(map[uint64]struct{}),
	}
	rt.teardownCond = sync.NewCond(&rt.teardownMu)
	// Populate synchronously so the worker does not listen until the pool is
	// ready (matches Dirigent). Avoids races where cold starts miss the pool.
	if cfg.UsePool {
		networks.populatePool()
		networks.startStatsLoop()
	}
	return rt, nil
}

func (r *Runtime) Prepare(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	if function == nil {
		return nil, status.Error(codes.InvalidArgument, "function is required")
	}
	image := function.GetRuntime().GetImage()
	if image == "" {
		image = r.cfg.RootfsImagePath
	}
	if image == "" {
		return nil, status.Error(codes.InvalidArgument, "function runtime image or firecracker rootfs_image_path is required")
	}
	if ok, err := r.HasImage(ctx, image); err != nil {
		return nil, err
	} else if !ok {
		if r.cfg.ArtifactsBucket != "" {
			objectName := filepath.Base(image)
			if err := runtime.DownloadFromGCS(ctx, r.cfg.ArtifactsBucket, objectName, image, r.logger); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to download rootfs image from GCS bucket %s: %v", r.cfg.ArtifactsBucket, err)
			}
		} else {
			return nil, status.Errorf(codes.NotFound, "firecracker rootfs image %q not found", image)
		}
	}
	return &core.PreparedArtifact{FunctionId: function.GetFunctionId(), Image: image}, nil
}

func (r *Runtime) HasImage(ctx context.Context, image string) (bool, error) {
	_ = ctx
	if image == "" {
		image = r.cfg.RootfsImagePath
	}
	if image == "" {
		return false, nil
	}
	if !filepath.IsAbs(image) {
		if _, err := os.Stat(image); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	_, err := os.Stat(image)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (r *Runtime) Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	function := req.GetFunction()
	if function == nil {
		return nil, status.Error(codes.InvalidArgument, "function is required")
	}
	if function.GetRuntime() == nil {
		return nil, status.Error(codes.InvalidArgument, "function runtime is required")
	}
	instanceID := req.GetInstanceId()
	if instanceID == 0 {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	r.mu.Lock()
	if _, ok := r.instances[instanceID]; ok {
		r.mu.Unlock()
		return nil, status.Errorf(codes.AlreadyExists, "firecracker instance %d already exists", instanceID)
	}
	if _, ok := r.starting[instanceID]; ok {
		r.mu.Unlock()
		return nil, status.Errorf(codes.AlreadyExists, "firecracker instance %d is already starting", instanceID)
	}
	if _, ok := r.reaping[instanceID]; ok {
		r.mu.Unlock()
		return nil, status.Errorf(codes.Unavailable, "firecracker instance %d is still being reaped", instanceID)
	}
	r.starting[instanceID] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.starting, instanceID)
		r.mu.Unlock()
	}()

	startCtx, cancel := context.WithTimeout(ctx, r.cfg.StartTimeout)
	defer cancel()
	if err := r.waitForAsyncTeardown(startCtx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "waiting for async teardown: %v", err)
	}
	network, err := r.networks.acquire(instanceID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "create firecracker network: %v", err)
	}
	cleanup := true
	discardNetwork := false
	defer func() {
		if cleanup {
			if discardNetwork {
				_ = r.networks.discard(network)
			} else {
				_ = r.networks.release(network)
			}
		}
	}()

	image := function.GetRuntime().GetImage()
	if image == "" && req.GetArtifact() != nil {
		image = req.GetArtifact().GetImage()
	}
	boot, err := prepareBootImage(r.cfg, image, instanceID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}
	defer func() {
		if cleanup && boot.workDir != "" && r.cfg.RootfsMode == "copy" {
			_ = os.RemoveAll(boot.workDir)
		}
	}()

	memMiB, vcpuCount := resourceShape(function)
	machineCfg := buildFirecrackerConfig(r.cfg, req, network, boot, memMiB, vcpuCount)
	controller := controllerAddress(r.cfg.WorkerListenAddress, network.HostIP.String())
	metadata := metadataFor(function, req, controller)

	var snapshot *snapshotMetadata
	functionKey := strconv.FormatUint(function.GetFunctionId(), 10)
	if r.cfg.UseSnapshots {
		if found, ok := r.snapshots.get(functionKey); ok {
			snapshot = &found
			r.logger.Info("firecracker snapshot restore started", "function_id", function.GetFunctionId(), "instance_id", instanceID)
		}
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	machine, restoredFromSnapshot, err := r.startMachine(stopCtx, startCtx, machineCfg, metadata, snapshot)
	if err == nil && restoredFromSnapshot {
		r.logger.Info("firecracker snapshot restore completed", "function_id", function.GetFunctionId(), "instance_id", instanceID)
	}
	if err != nil && restoredFromSnapshot {
		r.logger.Warn(
			"firecracker snapshot restore failed, invalidating and cold booting",
			"function_id", function.GetFunctionId(),
			"instance_id", instanceID,
			"error", err,
		)
		exited, cleanupErr := r.cleanupFailedMachine(machine, machineCfg.SocketPath, function.GetFunctionId(), instanceID)
		if !exited {
			// StopVMM has been requested but the process still owns its network and
			// rootfs. A reaper releases them only after the process exits.
			cleanup = false
			r.markReaping(instanceID)
			stopCancel()
			r.reapFailedMachine(machine, network, boot, function.GetFunctionId(), instanceID)
			return nil, status.Errorf(codes.Unavailable, "clean up failed snapshot restore: %v", cleanupErr)
		}
		if cleanupErr != nil {
			stopCancel()
			return nil, status.Errorf(codes.Unavailable, "clean up failed snapshot restore: %v", cleanupErr)
		}
		r.snapshots.invalidate(function.GetFunctionId())
		snapshot = nil
		machine, restoredFromSnapshot, err = r.startMachine(stopCtx, startCtx, machineCfg, metadata, nil)
	}
	if err != nil {
		exited, _ := r.cleanupFailedMachine(machine, machineCfg.SocketPath, function.GetFunctionId(), instanceID)
		if !exited {
			discardNetwork = true
			r.logger.Warn("failed-start VMM did not exit; discarding network", "instance_id", instanceID)
		}
		stopCancel()
		return nil, status.Errorf(codes.Unavailable, "%v", err)
	}
	if cpuAccountingEnabled() && machine != nil {
		if pid, pidErr := machine.PID(); pidErr == nil {
			if cpuNs, cpuErr := runtime.CPUNanoseconds(pid); cpuErr == nil {
				r.logger.Info("firecracker sandbox restore cpu",
					"instance_id", instanceID,
					"restored_from_snapshot", restoredFromSnapshot,
					"firecracker_cpu_ms", float64(cpuNs)/1e6,
				)
			}
		}
	}

	targetAddress := net.JoinHostPort(network.ExposedIP.String(), strconv.Itoa(functionPort))
	proxy, address, err := startPortProxy(
		stopCtx,
		localListenIP(r.cfg.WorkerListenAddress),
		r.cfg.ProxyAdvertiseAddress,
		targetAddress,
		r.logger,
		r.cfg.InternalCIDR,
		r.cfg.ExposedCIDR,
	)
	if err != nil {
		stopCancel()
		if !r.stopVMMAndWait(machine) {
			discardNetwork = true
		}
		return nil, status.Errorf(codes.Unavailable, "%v", err)
	}

	ready := false
	if restoredFromSnapshot {
		if err := waitForTCP(startCtx, targetAddress, 50*time.Millisecond); err != nil {
			stopCancel()
			proxy.close()
			if !r.stopVMMAndWait(machine) {
				discardNetwork = true
			}
			return nil, status.Errorf(codes.Unavailable, "restored sandbox port not reachable: %v", err)
		}
		ready = true
	}

	inst := &vmInstance{
		instanceID: instanceID,
		functionID: function.GetFunctionId(),
		machine:    machine,
		network:    network,
		proxy:      proxy,
		rootfsPath: boot.rootfsPath,
		initrdPath: boot.initrdPath,
		workDir:    boot.workDir,
		address:    address,
		startedAt:  time.Now(),
		memMiB:     memMiB,
		vcpuCount:  vcpuCount,
		stopCancel: stopCancel,
	}

	if r.cfg.UseSnapshots && !restoredFromSnapshot {
		if err := waitForTCP(startCtx, targetAddress, 50*time.Millisecond); err != nil {
			r.logger.Warn("firecracker snapshot skipped because function port was not reachable", "function_id", function.GetFunctionId(), "instance_id", instanceID, "error", err)
		} else if err := r.ensureSnapshot(startCtx, function.GetFunctionId(), boot.rootfsPath, machine); err != nil {
			r.logger.Warn("firecracker snapshot creation failed", "function_id", function.GetFunctionId(), "instance_id", instanceID, "error", err)
		}
	}

	r.mu.Lock()
	delete(r.starting, instanceID)
	r.instances[instanceID] = inst
	r.mu.Unlock()
	cleanup = false

	protocol := function.GetRuntime().GetProtocol()
	if protocol == "" {
		protocol = "grpc"
	}
	return &core.InstanceState{
		InstanceId: instanceID,
		FunctionId: function.GetFunctionId(),
		WorkerId:   req.GetWorkerId(),
		Address:    address,
		Protocol:   protocol,
		Ready:      ready,
		StartedAt:  timestamppb.Now(),
		Usage: &core.ResourceUsage{
			MemoryBytes: uint64(memMiB) * 1024 * 1024,
			CpuUnits:    uint64(vcpuCount) * 1000,
		},
	}, nil
}

func (r *Runtime) cleanupFailedMachine(machine *fc.Machine, socketPath string, functionID, instanceID uint64) (bool, error) {
	if machine != nil {
		if err := machine.StopVMM(); err != nil && !isProcessGone(err) {
			r.logger.Warn("failed to stop firecracker after failed start", "function_id", functionID, "instance_id", instanceID, "error", err)
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), failedMachineCleanupTimeout)
		err := machine.Wait(cleanupCtx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return false, fmt.Errorf("wait for firecracker process exit: %w", err)
		}
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		r.logger.Warn("failed to remove firecracker socket after failed start", "function_id", functionID, "instance_id", instanceID, "socket", socketPath, "error", err)
		return true, fmt.Errorf("remove firecracker socket: %w", err)
	}
	return true, nil
}

func (r *Runtime) reapFailedMachine(machine *fc.Machine, network *networkConfig, boot bootImage, functionID, instanceID uint64) {
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.reaping, instanceID)
			r.mu.Unlock()
		}()
		if machine != nil {
			_ = machine.Wait(context.Background())
		}
		_ = r.networks.release(network)
		if boot.workDir != "" && r.cfg.RootfsMode == "copy" {
			_ = os.RemoveAll(boot.workDir)
		}
		r.logger.Info("reaped firecracker resources after failed start", "function_id", functionID, "instance_id", instanceID)
	}()
}

func (r *Runtime) markReaping(instanceID uint64) {
	r.mu.Lock()
	delete(r.starting, instanceID)
	r.reaping[instanceID] = struct{}{}
	r.mu.Unlock()
}

func (r *Runtime) Stop(ctx context.Context, instanceID uint64) error {
	inst, err := r.instance(instanceID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.instances, instanceID)
	r.mu.Unlock()
	r.runTeardown(inst, ctx)
	return nil
}

func (r *Runtime) Stats(ctx context.Context, instanceID uint64) (*core.ResourceUsage, error) {
	_ = ctx
	inst, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	usage := &core.ResourceUsage{
		MemoryBytes: uint64(inst.memMiB) * 1024 * 1024,
		CpuUnits:    uint64(inst.vcpuCount) * 1000,
	}
	pid, err := inst.machine.PID()
	if err != nil {
		return usage, nil
	}
	if rss, err := runtime.RSSBytes(pid); err == nil && rss > 0 {
		usage.MemoryBytes = rss
	}
	if cpuNs, err := runtime.CPUNanoseconds(pid); err == nil && cpuNs > 0 {
		usage.CpuUnits = cpuNs
	}
	return usage, nil
}

func (r *Runtime) WatchLifecycle(ctx context.Context, instanceID uint64) (<-chan runtime.LifecycleEvent, error) {
	inst, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	out := make(chan runtime.LifecycleEvent, 1)
	go func() {
		defer close(out)
		err := inst.machine.Wait(ctx)
		event := runtime.LifecycleExit
		if err != nil && !errors.Is(err, context.Canceled) {
			event = runtime.LifecycleCrash
		}
		select {
		case out <- event:
		case <-ctx.Done():
		}
		r.mu.Lock()
		delete(r.instances, instanceID)
		r.mu.Unlock()
		r.runTeardown(inst, ctx)
	}()
	return out, nil
}

func (r *Runtime) releaseInstance(inst *vmInstance) error {
	if inst == nil {
		return nil
	}
	var releaseErr error
	inst.releaseOnce.Do(func() {
		if inst.discardNetwork {
			releaseErr = r.networks.discard(inst.network)
			return
		}
		releaseErr = r.networks.release(inst.network)
	})
	return releaseErr
}

// waitVMMExit waits until the firecracker process exits. It returns true when
// the process is gone. A non-nil error from Machine.Wait means either the
// process exited with an error (fatalErr) or the context expired; only the
// latter means it is still running.
func waitVMMExit(machine *fc.Machine, timeout time.Duration) bool {
	if machine == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := machine.Wait(ctx)
	return !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
}

// stopVMMAndWait force-stops the VMM and waits for it to exit. It returns true
// when the process is gone. Callers must discard the network when it returns
// false, because a live process still holds its TAP device.
func (r *Runtime) stopVMMAndWait(machine *fc.Machine) bool {
	if machine == nil {
		return true
	}
	_ = machine.StopVMM()
	return waitVMMExit(machine, failedMachineCleanupTimeout)
}

func (r *Runtime) runTeardown(inst *vmInstance, ctx context.Context) {
	inst.teardownOnce.Do(func() {
		if r.cfg.AsyncTeardown {
			r.beginAsyncTeardown()
			inst.stopCancel()
			inst.proxy.close()
			go func() {
				defer r.endAsyncTeardown()
				teardownCtx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
				defer cancel()
				if shutdownErr := r.shutdownInstance(inst, teardownCtx); shutdownErr != nil {
					r.logger.Warn("firecracker async teardown failed", "instance_id", inst.instanceID, "error", shutdownErr)
				}
			}()
			return
		}
		inst.stopCancel()
		inst.proxy.close()
		if shutdownErr := r.shutdownInstance(inst, ctx); shutdownErr != nil {
			r.logger.Warn("firecracker teardown failed", "instance_id", inst.instanceID, "error", shutdownErr)
		}
	})
}

func (r *Runtime) shutdownInstance(inst *vmInstance, ctx context.Context) error {
	var vmCPUNs uint64
	if inst.machine != nil {
		if pid, pidErr := inst.machine.PID(); pidErr == nil {
			vmCPUNs, _ = runtime.CPUNanoseconds(pid)
		}
	}
	shutdownErr := inst.machine.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = inst.machine.StopVMM()
	}
	// A TAP device is held until its firecracker process exits. Do not return
	// the network to the pool before then, or the next restore fails with
	// "Open tap device failed: Resource busy". If the process will not exit,
	// destroy the network instead of pooling it.
	exited := waitVMMExit(inst.machine, vmmGracefulExitTimeout)
	if !exited {
		_ = inst.machine.StopVMM()
		exited = waitVMMExit(inst.machine, failedMachineCleanupTimeout)
	}
	if !exited {
		inst.discardNetwork = true
		r.logger.Warn("firecracker VMM did not exit before teardown; discarding network",
			"instance_id", inst.instanceID)
	}
	if err := r.releaseInstance(inst); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	if r.cfg.RootfsMode == "copy" && inst.workDir != "" {
		if err := os.RemoveAll(inst.workDir); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if cpuAccountingEnabled() {
		r.logger.Info("firecracker sandbox stop cpu",
			"instance_id", inst.instanceID,
			"vm_cpu_ms", float64(vmCPUNs)/1e6,
			"vmm_exited", exited,
		)
	}
	return shutdownErr
}

func (r *Runtime) instance(instanceID uint64) (*vmInstance, error) {
	r.mu.RLock()
	inst, ok := r.instances[instanceID]
	r.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "sandbox %d not found", instanceID)
	}
	return inst, nil
}

func localListenIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return ""
	}
	return host
}

func waitForTCP(ctx context.Context, address string, interval time.Duration) error {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	var lastErr error
	for {
		conn, err := net.DialTimeout("tcp", address, interval)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w: last dial error: %v", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func isProcessGone(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

func (r *Runtime) beginAsyncTeardown() {
	r.teardownMu.Lock()
	r.activeTeardowns++
	r.teardownMu.Unlock()
}

func (r *Runtime) endAsyncTeardown() {
	r.teardownMu.Lock()
	if r.activeTeardowns > 0 {
		r.activeTeardowns--
	}
	r.teardownCond.Broadcast()
	r.teardownMu.Unlock()
}

func (r *Runtime) startMachine(
	processCtx context.Context,
	opCtx context.Context,
	machineCfg fc.Config,
	metadata map[string]any,
	snapshot *snapshotMetadata,
) (*fc.Machine, bool, error) {
	machine, err := startMachine(processCtx, opCtx, r.cfg, machineCfg, metadata, snapshot, r.logger)
	return machine, snapshot != nil, err
}

func (r *Runtime) ensureSnapshot(ctx context.Context, functionID uint64, rootfsPath string, machine *fc.Machine) error {
	key := strconv.FormatUint(functionID, 10)
	job, isCreator := r.snapshots.beginCreate(key)
	if !isCreator {
		if job != nil {
			return r.snapshots.waitCreate(job)
		}
		return nil
	}

	err := createSnapshot(ctx, machine, r.snapshots, functionID, rootfsPath)
	r.snapshots.finishCreate(key, job, err)
	return err
}

func (r *Runtime) waitForAsyncTeardown(ctx context.Context) error {
	if !r.cfg.UsePool || !r.cfg.AsyncTeardown {
		return nil
	}
	done := make(chan struct{})
	go func() {
		r.teardownMu.Lock()
		defer r.teardownMu.Unlock()
		for r.activeTeardowns > 0 {
			r.teardownCond.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
