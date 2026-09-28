package firecracker

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	fc "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"hyperfaas-ideal-arch/pkg/core"
)

type vmInstance struct {
	instanceID   uint64
	functionID   uint64
	machine      *fc.Machine
	network      *networkConfig
	proxy        *portProxy
	rootfsPath   string
	initrdPath   string
	workDir      string
	address      string
	startedAt    time.Time
	memMiB       int64
	vcpuCount    int64
	stopCancel   context.CancelFunc
	releaseOnce  sync.Once
	teardownOnce sync.Once
	// discardNetwork is set when the VMM did not exit before teardown finished,
	// so the network must be destroyed rather than pooled (its TAP is still held).
	discardNetwork bool
}

type bootImage struct {
	rootfsPath string
	initrdPath string
	workDir    string
}

func buildFirecrackerConfig(cfg Config, req *core.StartSandboxRequest, network *networkConfig, image bootImage, memMiB int64, vcpuCount int64) fc.Config {
	instanceID := req.GetInstanceId()
	function := req.GetFunction()
	kernelArgs := strings.TrimSpace(cfg.KernelArgs)
	if kernelArgs == "" {
		kernelArgs = "console=ttyS0 reboot=k panic=1 pci=off nomodule random.trust_cpu=on ipv6.disable=1"
		if cfg.Debug {
			kernelArgs += " earlyprintk=serial,ttyS0,115200 keep_bootcon"
		}
		if image.rootfsPath != "" {
			kernelArgs += " root=/dev/vda rw"
		} else if image.initrdPath != "" {
			kernelArgs += " rdinit=/init"
		}
	}
	controller := controllerAddress(cfg.WorkerListenAddress, network.HostIP.String())
	env := map[string]string{
		"CONTROLLER_ADDRESS": controller,
		"INSTANCE_ID":        strconv.FormatUint(instanceID, 10),
		"FUNCTION_ID":        strconv.FormatUint(function.GetFunctionId(), 10),
	}
	for key, value := range function.GetRuntime().GetEnv() {
		env[key] = value
	}
	kernelArgs += fmt.Sprintf(" ip=%s::%s:255.255.255.252::eth0:off", network.GuestIP, network.GatewayIP)
	kernelArgs += fmt.Sprintf(" hyperfaas.controller=%s hyperfaas.instance_id=%d hyperfaas.function_id=%d", controller, instanceID, function.GetFunctionId())
	kernelArgs += " hyperfaas.env_b64=" + encodeKernelEnv(env)

	machineCfg := fc.Config{
		SocketPath:      filepath.Join(cfg.WorkDir, "instances", fmt.Sprintf("%d.socket", instanceID)),
		LogPath:         filepath.Join(cfg.WorkDir, "instances", fmt.Sprintf("%d.log", instanceID)),
		LogLevel:        firecrackerLogLevel(cfg.Debug),
		KernelImagePath: cfg.KernelImagePath,
		KernelArgs:      kernelArgs,
		MachineCfg: models.MachineConfiguration{
			MemSizeMib: fc.Int64(memMiB),
			VcpuCount:  fc.Int64(vcpuCount),
			Smt:        fc.Bool(false),
		},
		NetworkInterfaces: []fc.NetworkInterface{{
			StaticConfiguration: &fc.StaticNetworkConfiguration{
				HostDevName: network.TapName,
				MacAddress:  network.GuestMAC,
			},
			AllowMMDS: true,
		}},
		NetNS:       network.Path,
		MmdsAddress: net.ParseIP(cfg.MMDSAddress),
		MmdsVersion: fc.MMDSv2,
	}
	if image.initrdPath != "" {
		machineCfg.InitrdPath = image.initrdPath
	}
	if image.rootfsPath != "" {
		machineCfg.Drives = []models.Drive{{
			DriveID:      fc.String("rootfs"),
			PathOnHost:   fc.String(image.rootfsPath),
			IsReadOnly:   fc.Bool(cfg.RootfsMode == "readonly"),
			IsRootDevice: fc.Bool(true),
		}}
	}
	return machineCfg
}

func startMachine(processCtx context.Context, opCtx context.Context, cfg Config, machineCfg fc.Config, metadata map[string]any, snapshot *snapshotMetadata, logger *slog.Logger) (*fc.Machine, error) {
	cmdBuilder := fc.VMCommandBuilder{}.
		WithBin(cfg.FirecrackerBin).
		WithSocketPath(machineCfg.SocketPath)
	if cfg.Debug {
		cmdBuilder = cmdBuilder.WithStdout(os.Stdout).WithStderr(os.Stderr)
	}
	cmd := cmdBuilder.Build(processCtx)

	var opts []fc.Opt
	opts = append(opts, fc.WithProcessRunner(cmd))
	opts = append(opts, fc.WithLogger(logrus.NewEntry(logrus.New())))
	if snapshot != nil {
		opts = append(opts, fc.WithSnapshot(snapshot.MemoryPath, snapshot.SnapshotPath, func(sc *fc.SnapshotConfig) {
			sc.ResumeVM = true
		}))
	} else {
		opts = append(opts, withSetMetadataHandler(metadata))
	}

	machine, err := fc.NewMachine(processCtx, machineCfg, opts...)
	if err != nil {
		return nil, fmt.Errorf("create firecracker machine: %w", err)
	}
	if err := machine.Start(processCtx); err != nil {
		if logger != nil {
			logger.Warn("firecracker start failed", "socket", machineCfg.SocketPath, "error", err)
		}
		// A process may have been spawned before the error. Its TAP device is
		// held until it exits, so drain it here; otherwise the caller could
		// return a network to the pool while it is still in use.
		_ = machine.StopVMM()
		waitCtx, cancel := context.WithTimeout(context.Background(), vmmGracefulExitTimeout)
		_ = machine.Wait(waitCtx)
		cancel()
		return nil, fmt.Errorf("start firecracker machine: %w", err)
	}
	if snapshot != nil {
		if err := machine.SetMetadata(opCtx, metadata); err != nil {
			// The VM is already running at this point. Return it so the caller can
			// stop it before retrying the restore as a cold boot.
			return machine, fmt.Errorf("refresh snapshot metadata: %w", err)
		}
	}
	return machine, nil
}

func withSetMetadataHandler(metadata map[string]any) fc.Opt {
	return func(machine *fc.Machine) {
		machine.Handlers.FcInit = machine.Handlers.FcInit.Append(fc.NewSetMetadataHandler(metadata))
	}
}

func prepareBootImage(cfg Config, image string, instanceID uint64) (bootImage, error) {
	base := image
	if base == "" {
		if cfg.InitrdImagePath != "" {
			base = cfg.InitrdImagePath
		} else {
			base = cfg.RootfsImagePath
		}
	}
	if base == "" {
		return bootImage{}, fmt.Errorf("firecracker runtime: boot image is required")
	}
	if _, err := os.Stat(base); err != nil {
		return bootImage{}, fmt.Errorf("firecracker runtime: boot image %q: %w", base, err)
	}
	if isInitrdImage(base) {
		return bootImage{initrdPath: base}, nil
	}
	if cfg.RootfsMode == "readonly" {
		return bootImage{rootfsPath: base, workDir: filepath.Dir(base)}, nil
	}
	dir := filepath.Join(cfg.WorkDir, "instances", strconv.FormatUint(instanceID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return bootImage{}, fmt.Errorf("create instance rootfs dir: %w", err)
	}
	dst := filepath.Join(dir, "rootfs.ext4")
	if err := cloneOrCopyFile(base, dst); err != nil {
		return bootImage{}, err
	}
	return bootImage{rootfsPath: dst, workDir: dir}, nil
}

func isInitrdImage(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".cpio") || strings.HasSuffix(lower, ".cpio.gz") || strings.HasSuffix(lower, ".initrd") || strings.HasSuffix(lower, ".initrd.gz")
}

func cloneOrCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open rootfs source: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create rootfs copy: %w", err)
	}
	cloned := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())) == nil
	if !cloned {
		if _, err := in.Seek(0, 0); err != nil {
			_ = out.Close()
			return fmt.Errorf("seek rootfs source: %w", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return fmt.Errorf("copy rootfs: %w", err)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close rootfs copy: %w", err)
	}
	return nil
}

func createSnapshot(ctx context.Context, machine *fc.Machine, manager *snapshotManager, functionID uint64, rootfsPath string) error {
	key := strconv.FormatUint(functionID, 10)
	paths := manager.paths(functionID)
	paths.RootfsPath = rootfsPath
	tmpMemory := paths.MemoryPath + ".creating"
	tmpSnapshot := paths.SnapshotPath + ".creating"
	_ = os.Remove(tmpMemory)
	_ = os.Remove(tmpSnapshot)

	if err := machine.PauseVM(ctx); err != nil {
		return fmt.Errorf("pause VM for snapshot: %w", err)
	}
	if err := machine.CreateSnapshot(ctx, tmpMemory, tmpSnapshot); err != nil {
		_ = machine.ResumeVM(context.Background())
		_ = os.Remove(tmpMemory)
		_ = os.Remove(tmpSnapshot)
		return fmt.Errorf("create VM snapshot: %w", err)
	}
	if err := publishSnapshotFiles(tmpMemory, tmpSnapshot, paths.MemoryPath, paths.SnapshotPath); err != nil {
		_ = machine.ResumeVM(context.Background())
		return err
	}
	manager.put(key, paths)
	if err := machine.ResumeVM(ctx); err != nil {
		return fmt.Errorf("resume VM after snapshot: %w", err)
	}
	return nil
}

func publishSnapshotFiles(tmpMemory, tmpSnapshot, memoryPath, snapshotPath string) error {
	if err := os.Rename(tmpMemory, memoryPath); err != nil {
		_ = os.Remove(tmpMemory)
		_ = os.Remove(tmpSnapshot)
		return fmt.Errorf("publish snapshot memory file: %w", err)
	}
	if err := os.Rename(tmpSnapshot, snapshotPath); err != nil {
		_ = os.Remove(memoryPath)
		_ = os.Remove(tmpSnapshot)
		return fmt.Errorf("publish snapshot state file: %w", err)
	}
	return nil
}

// resourceShape returns the memory size in MiB and vCPU count for the VM.
//
// NOTE: Firecracker configures CPU limits solely by assigning integer vCPU counts.
// Consequently, fine-grained fractional CPU allocations (e.g., 250 or 500 millicores)
// are rounded up to the nearest whole integer of vCPUs (minimum 1). Within the guest VM,
// the process is allowed to consume up to 100% of the allocated vCPU threads. Hard fractional
// CPU limiting is not enforced unless external host-level cgroups (or jailer resource limits)
// are configured for the Firecracker processes.
func resourceShape(function *core.FunctionSpec) (int64, int64) {
	memMiB := defaultMemSizeMiB
	vcpuCount := defaultVCPUCount
	if spec := function.GetRuntime().GetResources(); spec != nil {
		if spec.GetMemoryBytes() > 0 {
			memMiB = int64((spec.GetMemoryBytes() + 1024*1024 - 1) / (1024 * 1024))
			if memMiB < 64 {
				memMiB = 64
			}
		}
		if spec.GetCpuUnits() > 0 {
			vcpuCount = int64((spec.GetCpuUnits() + 999) / 1000)
			if vcpuCount < 1 {
				vcpuCount = 1
			}
		}
	}
	return memMiB, vcpuCount
}

func metadataFor(function *core.FunctionSpec, req *core.StartSandboxRequest, controller string) map[string]any {
	env := map[string]string{
		"CONTROLLER_ADDRESS": controller,
		"INSTANCE_ID":        strconv.FormatUint(req.GetInstanceId(), 10),
		"FUNCTION_ID":        strconv.FormatUint(function.GetFunctionId(), 10),
	}
	for key, value := range function.GetRuntime().GetEnv() {
		env[key] = value
	}
	return map[string]any{
		"hyperfaas": map[string]any{
			"instance_id": req.GetInstanceId(),
			"function_id": function.GetFunctionId(),
			"worker_id":   req.GetWorkerId(),
			"env":         env,
		},
	}
}

func encodeKernelEnv(env map[string]string) string {
	var b strings.Builder
	first := true
	for key, value := range env {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(b.String()))
}

func firecrackerLogLevel(debug bool) string {
	if debug {
		return "Debug"
	}
	return "Info"
}

func controllerAddress(listenAddress, hostIP string) string {
	_, port, err := net.SplitHostPort(listenAddress)
	if err != nil || port == "" {
		port = strconv.Itoa(functionPort)
	}
	return net.JoinHostPort(hostIP, port)
}

func firecrackerAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
