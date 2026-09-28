package runc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
)

type isolatedNetns struct {
	Name    string
	Path    string
	GuestIP net.IP
	HostIP  net.IP
	Mode    string // "veth" or "ipvlan"
}

type portProxy struct {
	listener net.Listener
	done     chan struct{}
}

type Runtime struct {
	cfg         Config
	logger      *slog.Logger
	pool        chan *isolatedNetns
	mu          sync.Mutex
	active      map[uint64]*isolatedNetns
	proxies     map[uint64]*portProxy
	stopCancels map[uint64]context.CancelFunc
}

func New(cfg Config, logger *slog.Logger) (*Runtime, error) {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 15 * time.Second
	}

	var pool chan *isolatedNetns
	if cfg.NetworkIsolation && cfg.UsePool {
		if cfg.PoolSize <= 0 {
			cfg.PoolSize = 20
		}
		pool = make(chan *isolatedNetns, cfg.PoolSize)
	}

	rt := &Runtime{
		cfg:         cfg,
		logger:      logger,
		pool:        pool,
		active:      make(map[uint64]*isolatedNetns),
		proxies:     make(map[uint64]*portProxy),
		stopCancels: make(map[uint64]context.CancelFunc),
	}

	if cfg.NetworkIsolation && cfg.UsePool {
		go rt.populatePool()
	}

	return rt, nil
}

func (r *Runtime) populatePool() {
	r.logger.Info("pre-populating network namespace pool", "size", r.cfg.PoolSize)
	for i := 0; i < r.cfg.PoolSize; i++ {
		netnsInfo, err := r.createNamespace(uint64(i + 500000))
		if err != nil {
			r.logger.Error("failed to pre-populate network namespace", "index", i, "error", err)
			continue
		}
		r.pool <- netnsInfo
	}
	r.logger.Info("network namespace pool pre-population completed")
}

func (r *Runtime) Prepare(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	binaryPath := function.GetRuntime().GetImage()
	if binaryPath == "" {
		return nil, fmt.Errorf("function runtime image (binary path) is required")
	}
	if _, err := os.Stat(binaryPath); err != nil {
		if os.IsNotExist(err) && r.cfg.ArtifactsBucket != "" {
			objectName := filepath.Base(binaryPath)
			if err := runtime.DownloadFromGCS(ctx, r.cfg.ArtifactsBucket, objectName, binaryPath, r.logger); err != nil {
				return nil, fmt.Errorf("failed to download binary from GCS bucket %s: %w", r.cfg.ArtifactsBucket, err)
			}
			if err := os.Chmod(binaryPath, 0755); err != nil {
				return nil, fmt.Errorf("failed to chmod binary %s: %w", binaryPath, err)
			}
		} else {
			return nil, fmt.Errorf("function binary not found at %s: %w", binaryPath, err)
		}
	}
	return &core.PreparedArtifact{
		FunctionId: function.GetFunctionId(),
		Image:      binaryPath,
	}, nil
}

func (r *Runtime) HasImage(ctx context.Context, image string) (bool, error) {
	if _, err := os.Stat(image); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target)
	})
}

func (r *Runtime) controllerAddress() string {
	host, port, err := net.SplitHostPort(r.cfg.WorkerListenAddress)
	if err != nil {
		return r.cfg.WorkerListenAddress
	}
	if host == "0.0.0.0" || host == "" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return r.cfg.WorkerListenAddress
}

func (r *Runtime) Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	function := req.GetFunction()
	if function == nil {
		return nil, fmt.Errorf("function is required")
	}
	instanceID := req.GetInstanceId()
	if instanceID == 0 {
		return nil, fmt.Errorf("instance_id is required")
	}

	binaryPath := function.GetRuntime().GetImage()
	if binaryPath == "" {
		if req.Artifact != nil && req.Artifact.GetImage() != "" {
			binaryPath = req.Artifact.GetImage()
		}
	}
	if binaryPath == "" {
		return nil, fmt.Errorf("function binary path is required")
	}

	bundleDir := filepath.Join(r.cfg.WorkDir, "instances", strconv.FormatUint(instanceID, 10))
	rootfsDir := filepath.Join(bundleDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create rootfs dir: %w", err)
	}

	artifactInfo, err := os.Stat(binaryPath)
	if err != nil {
		os.RemoveAll(bundleDir)
		return nil, fmt.Errorf("failed to stat function artifact: %w", err)
	}
	if artifactInfo.IsDir() {
		if err := copyDir(binaryPath, rootfsDir); err != nil {
			os.RemoveAll(bundleDir)
			return nil, fmt.Errorf("failed to copy rootfs artifact: %w", err)
		}
	} else {
		dstBinary := filepath.Join(rootfsDir, "function")
		if err := copyFile(binaryPath, dstBinary); err != nil {
			os.RemoveAll(bundleDir)
			return nil, fmt.Errorf("failed to copy binary to rootfs: %w", err)
		}
	}

	var nsPath string
	var guestIP, hostIP net.IP
	var port int
	var address string
	var controllerAddr string
	var netnsInfo *isolatedNetns
	var proxy *portProxy
	var stopCancel context.CancelFunc

	if r.cfg.NetworkIsolation {
		if r.cfg.UsePool && r.pool != nil {
			select {
			case netnsInfo = <-r.pool:
				r.logger.Debug("borrowed netns from pool", "ns", netnsInfo.Name)
			default:
				r.logger.Info("netns pool empty, creating namespace on the fly")
				netnsInfo, err = r.createNamespace(instanceID)
				if err != nil {
					os.RemoveAll(bundleDir)
					return nil, err
				}
			}
		} else {
			netnsInfo, err = r.createNamespace(instanceID)
			if err != nil {
				os.RemoveAll(bundleDir)
				return nil, err
			}
		}

		nsPath = netnsInfo.Path
		guestIP = netnsInfo.GuestIP
		hostIP = netnsInfo.HostIP
		port = 50052

		targetAddress := net.JoinHostPort(guestIP.String(), "50052")
		var stopCtx context.Context
		stopCtx, stopCancel = context.WithCancel(context.Background())
		var proxyAddress string
		proxy, proxyAddress, err = r.startPortProxy(stopCtx, localListenIP(r.cfg.WorkerListenAddress), targetAddress)
		if err != nil {
			stopCancel()
			var isPoolNS bool
			var id uint64
			_, _ = fmt.Sscanf(netnsInfo.Name, "hf-runc-%d", &id)
			if id >= 500000 {
				isPoolNS = true
			}
			if r.cfg.UsePool && r.pool != nil && isPoolNS {
				select {
				case r.pool <- netnsInfo:
				default:
					r.deleteNamespace(netnsInfo)
				}
			} else {
				r.deleteNamespace(netnsInfo)
			}
			os.RemoveAll(bundleDir)
			return nil, err
		}

		r.mu.Lock()
		r.active[instanceID] = netnsInfo
		r.proxies[instanceID] = proxy
		r.stopCancels[instanceID] = stopCancel
		r.mu.Unlock()

		address = proxyAddress

		_, controllerPort, splitErr := net.SplitHostPort(r.cfg.WorkerListenAddress)
		if splitErr != nil {
			controllerAddr = net.JoinHostPort(hostIP.String(), "50052")
		} else {
			controllerAddr = net.JoinHostPort(hostIP.String(), controllerPort)
		}
	} else {
		port, err = getFreePort()
		if err != nil {
			os.RemoveAll(bundleDir)
			return nil, fmt.Errorf("failed to allocate free port: %w", err)
		}
		hostIP := localHostIP()
		if hostIP == "" {
			hostIP = "127.0.0.1"
		}
		address = hostIP + ":" + strconv.Itoa(port)
		controllerAddr = r.controllerAddress()
	}

	cleanupStartFailure := func() {
		if stopCancel != nil {
			stopCancel()
		}
		if proxy != nil {
			proxy.close()
		}
		if netnsInfo != nil {
			r.mu.Lock()
			delete(r.active, instanceID)
			delete(r.proxies, instanceID)
			delete(r.stopCancels, instanceID)
			r.mu.Unlock()

			var id uint64
			_, _ = fmt.Sscanf(netnsInfo.Name, "hf-runc-%d", &id)
			if r.cfg.UsePool && r.pool != nil && id >= 500000 {
				select {
				case r.pool <- netnsInfo:
				default:
					r.deleteNamespace(netnsInfo)
				}
			} else {
				r.deleteNamespace(netnsInfo)
			}
		}
	}

	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm",
		"CONTROLLER_ADDRESS=" + controllerAddr,
		"INSTANCE_ID=" + strconv.FormatUint(instanceID, 10),
		"FUNCTION_ID=" + strconv.FormatUint(function.GetFunctionId(), 10),
		"FUNCTION_PORT=" + strconv.Itoa(port),
	}
	for key, value := range function.GetRuntime().GetEnv() {
		env = append(env, key+"="+value)
	}

	linuxResources := map[string]interface{}{
		"devices": []map[string]interface{}{
			{
				"allow":  false,
				"access": "rwm",
			},
		},
	}
	if spec := function.GetRuntime().GetResources(); spec != nil {
		if spec.GetMemoryBytes() > 0 {
			linuxResources["memory"] = map[string]interface{}{
				"limit": spec.GetMemoryBytes(),
			}
		}
		if spec.GetCpuUnits() > 0 {
			period := uint64(100000)
			quota := int64(period * spec.GetCpuUnits() / 1000)
			linuxResources["cpu"] = map[string]interface{}{
				"shares": spec.GetCpuUnits(),
				"quota":  quota,
				"period": period,
			}
		}
	}

	ociConfig := map[string]interface{}{
		"ociVersion": "1.2.1",
		"process": map[string]interface{}{
			"terminal": false,
			"user": map[string]interface{}{
				"uid": 0,
				"gid": 0,
			},
			"args": []string{"/function"},
			"env":  env,
			"cwd":  "/",
			"capabilities": map[string]interface{}{
				"bounding": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
				"effective": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
				"permitted": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
			},
			"noNewPrivileges": true,
		},
		"root": map[string]interface{}{
			"path":     "rootfs",
			"readonly": false,
		},
		"hostname": "runc",
		"mounts": []map[string]interface{}{
			{
				"destination": "/proc",
				"type":        "proc",
				"source":      "proc",
			},
			{
				"destination": "/dev",
				"type":        "tmpfs",
				"source":      "tmpfs",
				"options":     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
			{
				"destination": "/dev/pts",
				"type":        "devpts",
				"source":      "devpts",
				"options":     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"},
			},
			{
				"destination": "/dev/shm",
				"type":        "tmpfs",
				"source":      "shm",
				"options":     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
			},
			{
				"destination": "/dev/mqueue",
				"type":        "mqueue",
				"source":      "mqueue",
				"options":     []string{"nosuid", "noexec", "nodev"},
			},
			{
				"destination": "/sys",
				"type":        "sysfs",
				"source":      "sysfs",
				"options":     []string{"nosuid", "noexec", "nodev", "ro"},
			},
			{
				"destination": "/sys/fs/cgroup",
				"type":        "cgroup",
				"source":      "cgroup",
				"options":     []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
			},
		},
		"linux": map[string]interface{}{
			"resources": linuxResources,
			"namespaces": func() []map[string]interface{} {
				ns := []map[string]interface{}{
					{"type": "pid"},
					{"type": "ipc"},
					{"type": "uts"},
					{"type": "mount"},
					{"type": "cgroup"},
				}
				if r.cfg.NetworkIsolation {
					ns = append(ns, map[string]interface{}{
						"type": "network",
						"path": nsPath,
					})
				}
				return ns
			}(),
			"maskedPaths": []string{
				"/proc/acpi",
				"/proc/asound",
				"/proc/kcore",
				"/proc/keys",
				"/proc/latency_stats",
				"/proc/timer_list",
				"/proc/timer_stats",
				"/proc/sched_debug",
				"/sys/firmware",
				"/proc/scsi",
			},
			"readonlyPaths": []string{
				"/proc/bus",
				"/proc/fs",
				"/proc/irq",
				"/proc/sys",
				"/proc/sysrq-trigger",
			},
		},
	}

	configJSON, err := json.MarshalIndent(ociConfig, "", "  ")
	if err != nil {
		cleanupStartFailure()
		os.RemoveAll(bundleDir)
		return nil, fmt.Errorf("failed to marshal config.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "config.json"), configJSON, 0644); err != nil {
		cleanupStartFailure()
		os.RemoveAll(bundleDir)
		return nil, fmt.Errorf("failed to write config.json: %w", err)
	}

	containerID := strconv.FormatUint(instanceID, 10)
	pidFile := filepath.Join(bundleDir, "pid")

	runcLogPath := filepath.Join(bundleDir, "runc.log")
	runcLog, err := os.OpenFile(runcLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		cleanupStartFailure()
		os.RemoveAll(bundleDir)
		return nil, fmt.Errorf("failed to create runc.log: %w", err)
	}
	defer runcLog.Close()

	cmd := exec.Command("runc", "run", "--detach", "--pid-file", pidFile, "--bundle", bundleDir, containerID)
	cmd.Stdout = runcLog
	cmd.Stderr = runcLog

	r.logger.Info("starting runc container", "container_id", containerID, "cmd", cmd.String())
	err = cmd.Run()
	if err != nil {
		logBytes, _ := os.ReadFile(runcLogPath)
		cleanupStartFailure()
		os.RemoveAll(bundleDir)
		return nil, fmt.Errorf("failed to start runc container: %w, output: %s", err, string(logBytes))
	}

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
		Ready:      false,
		StartedAt:  timestamppb.Now(),
	}, nil
}

func (r *Runtime) Stop(ctx context.Context, instanceID uint64) error {
	containerID := strconv.FormatUint(instanceID, 10)
	bundleDir := filepath.Join(r.cfg.WorkDir, "instances", containerID)

	// runc kill
	cmdKill := exec.Command("runc", "kill", containerID, "SIGKILL")
	_ = cmdKill.Run()

	// Wait for process to exit and retry runc delete
	var deleteErr error
	for i := 0; i < 50; i++ {
		cmdDel := exec.Command("runc", "delete", containerID)
		deleteErr = cmdDel.Run()
		if deleteErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if deleteErr != nil {
		r.logger.Warn("failed to delete runc container", "container_id", containerID, "error", deleteErr)
	}

	r.mu.Lock()
	netnsInfo := r.active[instanceID]
	delete(r.active, instanceID)
	proxy := r.proxies[instanceID]
	delete(r.proxies, instanceID)
	stopCancel := r.stopCancels[instanceID]
	delete(r.stopCancels, instanceID)
	r.mu.Unlock()

	if stopCancel != nil {
		stopCancel()
	}
	if proxy != nil {
		proxy.close()
	}

	if netnsInfo != nil {
		var isPoolNS bool
		var id uint64
		_, _ = fmt.Sscanf(netnsInfo.Name, "hf-runc-%d", &id)
		if id >= 500000 {
			isPoolNS = true
		}

		if r.cfg.UsePool && r.pool != nil && isPoolNS {
			select {
			case r.pool <- netnsInfo:
				r.logger.Debug("returned netns to pool", "ns", netnsInfo.Name)
			default:
				r.logger.Info("netns pool full, deleting namespace")
				r.deleteNamespace(netnsInfo)
			}
		} else {
			r.deleteNamespace(netnsInfo)
		}
	}

	// Clean up bundle directory
	_ = os.RemoveAll(bundleDir)
	return nil
}

type runcStats struct {
	Data struct {
		CPU struct {
			Usage struct {
				Total uint64 `json:"total"`
			} `json:"usage"`
		} `json:"cpu"`
		Memory struct {
			Usage struct {
				Usage uint64 `json:"usage"`
			} `json:"usage"`
		} `json:"memory"`
	} `json:"data"`
}

func (r *Runtime) Stats(ctx context.Context, instanceID uint64) (*core.ResourceUsage, error) {
	containerID := strconv.FormatUint(instanceID, 10)
	bundleDir := filepath.Join(r.cfg.WorkDir, "instances", containerID)
	pidFile := filepath.Join(bundleDir, "pid")

	pidBytes, err := os.ReadFile(pidFile)
	if err == nil {
		pid, err := strconv.ParseInt(strings.TrimSpace(string(pidBytes)), 10, 32)
		if err == nil && pid > 0 {
			rss, err1 := runtime.RSSBytes(int(pid))
			cpuNs, err2 := runtime.CPUNanoseconds(int(pid))
			if err1 == nil && err2 == nil {
				return &core.ResourceUsage{
					CpuUnits:    cpuNs,
					MemoryBytes: rss,
				}, nil
			}
		}
	}

	// Fallback to calling runc events if pid file is missing or reading stats failed
	cmd := exec.Command("runc", "events", "--stats", containerID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get runc stats: %w, output: %s", err, string(output))
	}

	var stats runcStats
	if err := json.Unmarshal(output, &stats); err != nil {
		return nil, fmt.Errorf("failed to parse runc stats JSON: %w, output: %s", err, string(output))
	}

	return &core.ResourceUsage{
		CpuUnits:    stats.Data.CPU.Usage.Total,
		MemoryBytes: stats.Data.Memory.Usage.Usage,
	}, nil
}

func (r *Runtime) WatchLifecycle(ctx context.Context, instanceID uint64) (<-chan runtime.LifecycleEvent, error) {
	containerID := strconv.FormatUint(instanceID, 10)
	bundleDir := filepath.Join(r.cfg.WorkDir, "instances", containerID)
	pidFile := filepath.Join(bundleDir, "pid")

	var pid int
	var err error
	for i := 0; i < 20; i++ {
		var pidBytes []byte
		pidBytes, err = os.ReadFile(pidFile)
		if err == nil {
			var p uint64
			p, err = strconv.ParseUint(string(pidBytes), 10, 32)
			if err == nil {
				pid = int(p)
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read container PID from %s: %w", pidFile, err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to find process: %w", err)
	}

	out := make(chan runtime.LifecycleEvent, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := process.Signal(syscall.Signal(0))
				if err != nil {
					select {
					case out <- runtime.LifecycleExit:
					default:
					}
					return
				}
			}
		}
	}()

	return out, nil
}

func (r *Runtime) createNamespace(instanceID uint64) (*isolatedNetns, error) {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	nsName := fmt.Sprintf("hf-runc-%d", instanceID)
	nsPath := filepath.Join("/var/run/netns", nsName)

	subnetIdx := int(instanceID % 16384)
	thirdOctet := byte((subnetIdx >> 6) & 0xFF)
	fourthOctet := byte((subnetIdx & 0x3F) << 2)

	hostIP := net.IPv4(10, 242, thirdOctet, fourthOctet+1)
	guestIP := net.IPv4(10, 242, thirdOctet, fourthOctet+2)

	_ = netns.DeleteNamed(nsName)

	origNS, err := netns.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get original netns: %w", err)
	}
	defer origNS.Close()

	newNS, err := netns.NewNamed(nsName)
	if err != nil {
		return nil, fmt.Errorf("failed to create named netns %s: %w", nsName, err)
	}
	defer newNS.Close()

	lo, err := netlink.LinkByName("lo")
	if err == nil {
		_ = netlink.LinkSetUp(lo)
	}

	if err := netns.Set(origNS); err != nil {
		return nil, fmt.Errorf("failed to return to original netns: %w", err)
	}

	mode := r.cfg.NetworkMode
	if mode == "" {
		mode = "veth"
	}

	if mode == "ipvlan" {
		parentInterface, err := getDefaultRouteInterface()
		if err != nil {
			return nil, err
		}

		parentLink, err := netlink.LinkByName(parentInterface)
		if err != nil {
			return nil, fmt.Errorf("failed to find parent link %s: %w", parentInterface, err)
		}

		// Ensure host-side IPVlan interface exists
		hostIpvlanName := "hf-ip-host"
		var hostIpvlanLink netlink.Link
		hostIpvlanLink, err = netlink.LinkByName(hostIpvlanName)
		if err != nil {
			// Link doesn't exist, create it
			ipvlanHost := &netlink.IPVlan{
				LinkAttrs: netlink.LinkAttrs{
					Name:        hostIpvlanName,
					ParentIndex: parentLink.Attrs().Index,
				},
				Mode: netlink.IPVLAN_MODE_L3,
			}
			if err := netlink.LinkAdd(ipvlanHost); err != nil {
				return nil, fmt.Errorf("failed to add host-side ipvlan device: %w", err)
			}
			hostIpvlanLink, err = netlink.LinkByName(hostIpvlanName)
			if err != nil {
				return nil, fmt.Errorf("failed to find host-side ipvlan device after creation: %w", err)
			}
		}

		// Ensure the host-side IPVlan has IP 10.242.255.254/16 assigned
		hostIpvlanIP := net.IPv4(10, 242, 255, 254)
		addrs, err := netlink.AddrList(hostIpvlanLink, netlink.FAMILY_V4)
		hasAddr := false
		if err == nil {
			for _, addr := range addrs {
				if addr.IPNet != nil && addr.IPNet.IP.Equal(hostIpvlanIP) {
					hasAddr = true
					break
				}
			}
		}
		if !hasAddr {
			hostAddr := &net.IPNet{
				IP:   hostIpvlanIP,
				Mask: net.CIDRMask(16, 32),
			}
			if err := netlink.AddrAdd(hostIpvlanLink, &netlink.Addr{IPNet: hostAddr}); err != nil {
				if !strings.Contains(err.Error(), "file exists") {
					return nil, fmt.Errorf("failed to assign IP to host-side ipvlan: %w", err)
				}
			}
		}

		// Ensure host IPVlan link is UP
		if err := netlink.LinkSetUp(hostIpvlanLink); err != nil {
			// Log or ignore if already UP
		}

		ipvlanLinkName := fmt.Sprintf("hfip-%08x", uint32(instanceID))
		if oldLink, err := netlink.LinkByName(ipvlanLinkName); err == nil {
			_ = netlink.LinkDel(oldLink)
		}

		ipvlan := &netlink.IPVlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        ipvlanLinkName,
				ParentIndex: parentLink.Attrs().Index,
			},
			Mode: netlink.IPVLAN_MODE_L3,
		}
		if err := netlink.LinkAdd(ipvlan); err != nil {
			return nil, fmt.Errorf("failed to add ipvlan device: %w", err)
		}

		link, err := netlink.LinkByName(ipvlanLinkName)
		if err != nil {
			return nil, fmt.Errorf("failed to find ipvlan link: %w", err)
		}
		if err := netlink.LinkSetNsFd(link, int(newNS)); err != nil {
			return nil, fmt.Errorf("failed to move ipvlan into namespace: %w", err)
		}

		if err := netns.Set(newNS); err != nil {
			return nil, fmt.Errorf("failed to switch to guest netns: %w", err)
		}

		guestNsLink, err := netlink.LinkByName(ipvlanLinkName)
		if err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to find ipvlan link inside netns: %w", err)
		}
		guestAddr := &net.IPNet{
			IP:   guestIP,
			Mask: net.CIDRMask(16, 32),
		}
		if err := netlink.AddrAdd(guestNsLink, &netlink.Addr{IPNet: guestAddr}); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to assign IP to ipvlan: %w", err)
		}
		if err := netlink.LinkSetUp(guestNsLink); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to set ipvlan up: %w", err)
		}

		route := &netlink.Route{
			Scope:     netlink.SCOPE_UNIVERSE,
			LinkIndex: guestNsLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.IPv4zero,
				Mask: net.CIDRMask(0, 32),
			},
		}
		if err := netlink.RouteAdd(route); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to add default route in ipvlan: %w", err)
		}

		if err := netns.Set(origNS); err != nil {
			return nil, fmt.Errorf("failed to return to original netns: %w", err)
		}

		return &isolatedNetns{
			Name:    nsName,
			Path:    nsPath,
			GuestIP: guestIP,
			HostIP:  hostIpvlanIP,
			Mode:    "ipvlan",
		}, nil

	} else {
		hostVethName := fmt.Sprintf("hfh-%08x", uint32(instanceID))
		guestVethName := fmt.Sprintf("hfg-%08x", uint32(instanceID))

		if oldLink, err := netlink.LinkByName(hostVethName); err == nil {
			_ = netlink.LinkDel(oldLink)
		}

		veth := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{
				Name: hostVethName,
				MTU:  1500,
			},
			PeerName: guestVethName,
		}
		if err := netlink.LinkAdd(veth); err != nil {
			return nil, fmt.Errorf("failed to add veth pair: %w", err)
		}

		guestLink, err := netlink.LinkByName(guestVethName)
		if err != nil {
			return nil, fmt.Errorf("failed to find guest veth end: %w", err)
		}

		if err := netlink.LinkSetNsFd(guestLink, int(newNS)); err != nil {
			return nil, fmt.Errorf("failed to move guest veth into namespace: %w", err)
		}

		hostLink, err := netlink.LinkByName(hostVethName)
		if err != nil {
			return nil, fmt.Errorf("failed to find host veth end: %w", err)
		}
		hostAddr := &net.IPNet{
			IP:   hostIP,
			Mask: net.CIDRMask(30, 32),
		}
		if err := netlink.AddrAdd(hostLink, &netlink.Addr{IPNet: hostAddr}); err != nil {
			return nil, fmt.Errorf("failed to assign IP to host veth: %w", err)
		}
		if err := netlink.LinkSetUp(hostLink); err != nil {
			return nil, fmt.Errorf("failed to set host veth up: %w", err)
		}

		if err := netns.Set(newNS); err != nil {
			return nil, fmt.Errorf("failed to switch to guest netns: %w", err)
		}

		guestNsLink, err := netlink.LinkByName(guestVethName)
		if err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to find guest veth inside namespace: %w", err)
		}
		guestAddr := &net.IPNet{
			IP:   guestIP,
			Mask: net.CIDRMask(30, 32),
		}
		if err := netlink.AddrAdd(guestNsLink, &netlink.Addr{IPNet: guestAddr}); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to assign IP to guest veth: %w", err)
		}
		if err := netlink.LinkSetUp(guestNsLink); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to set guest veth up: %w", err)
		}

		route := &netlink.Route{
			Scope:     netlink.SCOPE_UNIVERSE,
			LinkIndex: guestNsLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.IPv4zero,
				Mask: net.CIDRMask(0, 32),
			},
			Gw: hostIP,
		}
		if err := netlink.RouteAdd(route); err != nil {
			_ = netns.Set(origNS)
			return nil, fmt.Errorf("failed to add default route inside guest: %w", err)
		}

		if err := netns.Set(origNS); err != nil {
			return nil, fmt.Errorf("failed to return to original netns: %w", err)
		}

		return &isolatedNetns{
			Name:    nsName,
			Path:    nsPath,
			HostIP:  hostIP,
			GuestIP: guestIP,
			Mode:    "veth",
		}, nil
	}
}

func (r *Runtime) deleteNamespace(nsInfo *isolatedNetns) {
	if nsInfo.Mode == "ipvlan" {
		_ = netns.DeleteNamed(nsInfo.Name)
	} else {
		var instanceID uint64
		_, _ = fmt.Sscanf(nsInfo.Name, "hf-runc-%d", &instanceID)
		if instanceID > 0 {
			hostVethName := fmt.Sprintf("hfh-%08x", uint32(instanceID))
			if hostLink, err := netlink.LinkByName(hostVethName); err == nil {
				_ = netlink.LinkDel(hostLink)
			}
		}
		_ = netns.DeleteNamed(nsInfo.Name)
	}
}

func getDefaultRouteInterface() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err == nil {
		for _, route := range routes {
			isDefault := false
			if route.Dst == nil || route.Dst.IP.IsUnspecified() {
				isDefault = true
			} else {
				ones, _ := route.Dst.Mask.Size()
				if ones == 0 {
					isDefault = true
				}
			}
			if isDefault {
				link, err := netlink.LinkByIndex(route.LinkIndex)
				if err == nil {
					return link.Attrs().Name, nil
				}
			}
		}
	}

	// Fallback: search links for active physical/virtual interface
	links, err := netlink.LinkList()
	if err == nil {
		for _, link := range links {
			name := link.Attrs().Name
			if (strings.HasPrefix(name, "en") || strings.HasPrefix(name, "et")) &&
				name != "lo" &&
				!strings.HasPrefix(name, "veth") &&
				!strings.HasPrefix(name, "br-") &&
				!strings.HasPrefix(name, "docker") &&
				!strings.HasPrefix(name, "cni") {
				return name, nil
			}
		}
	}

	return "", fmt.Errorf("default route interface not found")
}

func localHostIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	// First pass: look for physical/primary interfaces like eth* or ens*
	for _, iface := range ifaces {
		name := iface.Name
		if len(name) >= 3 && (name[:3] == "eth" || name[:3] == "ens") {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP.String()
					}
				}
			}
		}
	}
	// Second pass: fallback to any non-virtual interface
	for _, iface := range ifaces {
		name := iface.Name
		if len(name) >= 3 && (name[:3] == "tap" || name[:3] == "veth" || name[:3] == "br-") {
			continue
		}
		if len(name) >= 6 && name[:6] == "docker" {
			continue
		}
		if name == "lo" {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	return ""
}

func (r *Runtime) startPortProxy(ctx context.Context, listenIP string, target string) (*portProxy, string, error) {
	bindIP := listenIP
	if bindIP == "" || bindIP == "0.0.0.0" || bindIP == "::" {
		bindIP = "0.0.0.0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindIP, "0"))
	if err != nil {
		return nil, "", fmt.Errorf("listen for runc port proxy: %w", err)
	}

	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var returnIP string
	if listenIP == "" || listenIP == "0.0.0.0" || listenIP == "::" {
		returnIP = localHostIP()
		if returnIP == "" {
			returnIP = "127.0.0.1"
		}
	} else {
		returnIP = listenIP
	}
	address := net.JoinHostPort(returnIP, portStr)

	proxy := &portProxy{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(proxy.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				targetConn, err := net.DialTimeout("tcp", target, 500*time.Millisecond)
				if err != nil {
					return
				}
				defer targetConn.Close()
				errCh := make(chan error, 2)
				go copyConn(errCh, targetConn, client)
				go copyConn(errCh, client, targetConn)
				select {
				case <-errCh:
				case <-ctx.Done():
				}
			}(conn)
		}
	}()
	return proxy, address, nil
}

func copyConn(errCh chan<- error, dst net.Conn, src net.Conn) {
	_, err := io.Copy(dst, src)
	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	errCh <- err
}

func (p *portProxy) close() {
	if p == nil {
		return
	}
	_ = p.listener.Close()
	<-p.done
}

func localListenIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return ""
	}
	return host
}
