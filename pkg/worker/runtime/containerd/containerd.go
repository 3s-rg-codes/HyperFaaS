package containerd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	v1 "github.com/containerd/cgroups/stats/v1"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/go-cni"
	"github.com/containerd/typeurl/v2"
	"github.com/coreos/go-iptables/iptables"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vishvananda/netns"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
)

const (
	functionPort    = 50052
	portPoolBase    = 32768
	teardownTimeout = 60 * time.Second

	// netnsMountDir is where named network namespaces are bind-mounted. Pool
	// namespaces are named "hf-cni-*" under this directory.
	netnsMountDir = "/var/run/netns"
	netnsPrefix   = "hf-cni-"
)

// Config configures the containerd runtime.
type Config struct {
	CRIPath             string
	CNIConfigPath       string
	Namespace           string
	PrefetchImage       bool
	WorkerListenAddress string

	// UsePool enables network namespace pooling (Veth Pooling) to bypass CNI setup serialization.
	// Resolves kernel mount/namespace lock contention during concurrent cold starts.
	UsePool  bool
	PoolSize int

	// AsyncTeardown defers CNI cleanup and containerd resource deletion to a background goroutine.
	// Resolves synchronous blocking in container stop operations.
	AsyncTeardown bool

	// SingleFlightPull deduplicates concurrent image pull requests for the same image.
	// Resolves network redundancy and registry rate-limiting during cold storms.
	SingleFlightPull bool
}

// ContainerMetadata tracks runtime metadata for active containerd sandboxes.
type ContainerMetadata struct {
	Container containerd.Container
	Task      containerd.Task
	HostPort  int
	GuestIP   string
	NetNs     string
	PooledNS  *pooledNetns
}

type pooledNetns struct {
	Name string
	Path string
	IP   string
}

type pooledPort struct {
	port     int
	listener net.Listener
}

type pullRequest struct {
	wg  sync.WaitGroup
	err error
	img containerd.Image
}

// Runtime manages function sandboxes using a local containerd daemon directly.
// This runtime is adapted to mirror Dirigent's containerd runtime for comparability.
type Runtime struct {
	client    *containerd.Client
	cniClient cni.CNI
	ipt       *iptables.IPTables
	cfg       Config
	logger    *slog.Logger

	mu         sync.RWMutex
	containers map[uint64]*ContainerMetadata
	images     sync.Map // cache for imageRef -> containerd.Image

	pool       chan *pooledNetns
	portPool   chan *pooledPort
	pullJobs   map[string]*pullRequest
	pullJobsMu sync.Mutex

	poolIDCounter   uint64
	cniReplenishMu  sync.Mutex
	portReplenishMu sync.Mutex
	nextPort        atomic.Int32
	teardownMu      sync.Mutex
	teardownCond    *sync.Cond
	activeTeardowns int

	// deleteNetns removes a named network namespace bind mount. It is a field so
	// tests can observe namespace teardown without root or a live runtime.
	deleteNetns func(string) error
}

// New creates and initializes a new containerd Runtime.
// Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/runtime.go -> NewContainerdRuntime
func New(cfg Config, logger *slog.Logger) (*Runtime, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "cm"
	}

	client, err := containerd.New(cfg.CRIPath)
	if err != nil {
		return nil, fmt.Errorf("containerd runtime: failed to connect to containerd socket at %s: %w", cfg.CRIPath, err)
	}

	cniClient, err := cni.New(cni.WithConfFile(cfg.CNIConfigPath))
	if err != nil {
		return nil, fmt.Errorf("containerd runtime: failed to initialize CNI client with config %s: %w", cfg.CNIConfigPath, err)
	}

	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("containerd runtime: failed to initialize iptables: %w", err)
	}

	rt := &Runtime{
		client:     client,
		cniClient:  cniClient,
		ipt:        ipt,
		cfg:        cfg,
		logger:     logger,
		containers: make(map[uint64]*ContainerMetadata),
		pullJobs:   make(map[string]*pullRequest),
	}
	rt.teardownCond = sync.NewCond(&rt.teardownMu)
	rt.deleteNetns = netns.DeleteNamed

	// Best-effort startup sweep: remove named namespaces left behind by a
	// previous worker process before the pool is (re)populated. Without this,
	// on-the-fly "hf-cni-<instanceID>" namespaces from a crashed/restarted
	// worker would never be reused and would accumulate until the bridge
	// rejects new veths.
	rt.sweepOrphanNetns(netnsMountDir, netnsPrefix, nil)

	if cfg.UsePool {
		if cfg.PoolSize <= 0 {
			cfg.PoolSize = 32
		}
		rt.cfg = cfg
		rt.pool = make(chan *pooledNetns, cfg.PoolSize)
		rt.portPool = make(chan *pooledPort, cfg.PoolSize)
		atomic.StoreUint64(&rt.poolIDCounter, uint64(cfg.PoolSize-1))
		rt.nextPort.Store(int32(portPoolBase + cfg.PoolSize))
		go rt.populateCNIPool()
		go rt.populatePortPool()
	}

	if cfg.PrefetchImage {
		ctx := namespaces.WithNamespace(context.Background(), cfg.Namespace)
		// Prefetch empty/trace functions to match Dirigent's baseline
		for _, img := range []string{
			"docker.io/cvetkovic/dirigent_empty_function:latest",
			"docker.io/cvetkovic/dirigent_trace_function:latest",
		} {
			logger.Info("prefetching image", "image", img)
			if _, err := rt.resolveImage(ctx, img); err != nil {
				logger.Warn("failed to prefetch image", "image", img, "error", err)
			}
		}
	}

	return rt, nil
}

func (r *Runtime) populateCNIPool() {
	r.logger.Info("pre-populating CNI namespace pool", "size", r.cfg.PoolSize)
	for i := 0; i < r.cfg.PoolSize; i++ {
		netnsInfo, err := r.createCNINamespace(uint64(i + 800000))
		if err != nil {
			r.logger.Error("failed to pre-populate CNI network namespace", "index", i, "error", err)
			continue
		}
		r.pool <- netnsInfo
	}
	r.logger.Info("CNI network namespace pool pre-population completed")
}

func (r *Runtime) populatePortPool() {
	r.logger.Info("pre-populating host port pool", "size", r.cfg.PoolSize, "base", portPoolBase)
	for i := 0; i < r.cfg.PoolSize; i++ {
		pp, err := r.reservePort(portPoolBase + i)
		if err != nil {
			r.logger.Error("failed to pre-populate host port pool", "port", portPoolBase+i, "error", err)
			continue
		}
		r.portPool <- pp
	}
	r.logger.Info("host port pool pre-population completed")
}

func (r *Runtime) reservePort(port int) (*pooledPort, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return &pooledPort{port: port, listener: l}, nil
}

func (r *Runtime) borrowHostPort() (int, error) {
	if r.portPool == nil {
		return r.getFreePort()
	}
	select {
	case pp := <-r.portPool:
		port := pp.port
		_ = pp.listener.Close()
		return port, nil
	default:
		port := int(r.nextPort.Add(1) - 1)
		r.logger.Info("host port pool empty, reserving port on demand", "port", port)
		pp, err := r.reservePort(port)
		if err != nil {
			return 0, err
		}
		_ = pp.listener.Close()
		return pp.port, nil
	}
}

func (r *Runtime) returnHostPort(port int) {
	if r.portPool == nil {
		return
	}
	pp, err := r.reservePort(port)
	if err != nil {
		r.logger.Warn("failed to return host port to pool", "port", port, "error", err)
		r.triggerPortReplenish()
		return
	}
	select {
	case r.portPool <- pp:
		r.triggerPortReplenish()
	default:
		_ = pp.listener.Close()
	}
}

func (r *Runtime) triggerPortReplenish() {
	if r.portPool == nil {
		return
	}
	go r.replenishPortPool()
}

func (r *Runtime) replenishPortPool() {
	if r.portPool == nil {
		return
	}
	if !r.portReplenishMu.TryLock() {
		return
	}
	defer r.portReplenishMu.Unlock()

	for len(r.portPool) < cap(r.portPool) {
		port := int(r.nextPort.Add(1) - 1)
		pp, err := r.reservePort(port)
		if err != nil {
			r.logger.Warn("failed to replenish host port pool", "port", port, "error", err)
			return
		}
		select {
		case r.portPool <- pp:
			r.logger.Debug("replenished host port pool", "port", port)
		default:
			_ = pp.listener.Close()
			return
		}
	}
}

func (r *Runtime) triggerCNIPoolReplenish() {
	if r.pool == nil {
		return
	}
	go r.replenishCNIPool()
}

func (r *Runtime) replenishCNIPool() {
	if r.pool == nil {
		return
	}
	if !r.cniReplenishMu.TryLock() {
		return
	}
	defer r.cniReplenishMu.Unlock()

	for len(r.pool) < cap(r.pool) {
		id := atomic.AddUint64(&r.poolIDCounter, 1)
		netnsInfo, err := r.createCNINamespace(800000 + id)
		if err != nil {
			r.logger.Warn("failed to replenish CNI pool", "error", err)
			return
		}
		select {
		case r.pool <- netnsInfo:
			r.logger.Debug("replenished CNI pool", "ns", netnsInfo.Name)
		default:
			r.destroyCNINamespace(netnsInfo)
			return
		}
	}
}

func (r *Runtime) returnNetnsToPool(netnsInfo *pooledNetns) {
	if netnsInfo == nil {
		return
	}
	if r.pool != nil {
		select {
		case r.pool <- netnsInfo:
			r.logger.Debug("returned netns to pool", "ns", netnsInfo.Name)
			return
		default:
		}
	}
	// No pool, or the pool is full: never drop the namespace. Destroy it so
	// orphaned namespaces cannot accumulate unboundedly.
	r.destroyCNINamespace(netnsInfo)
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

func (r *Runtime) createCNINamespace(id uint64) (*pooledNetns, error) {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	nsName := fmt.Sprintf(netnsPrefix+"%d", id)
	nsPath := filepath.Join(netnsMountDir, nsName)

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
	newNS.Close()
	if err := netns.Set(origNS); err != nil {
		_ = netns.DeleteNamed(nsName)
		return nil, fmt.Errorf("failed to restore original netns after creating %s: %w", nsName, err)
	}

	ctx := namespaces.WithNamespace(context.Background(), r.cfg.Namespace)
	result, err := r.cniClient.Setup(ctx, nsName, nsPath)
	if err != nil {
		_ = netns.DeleteNamed(nsName)
		return nil, fmt.Errorf("failed to CNI setup netns %s: %w", nsName, err)
	}

	var ip string
	if eth0, ok := result.Interfaces["eth0"]; ok && len(eth0.IPConfigs) > 0 && eth0.IPConfigs[0].IP != nil {
		ip = eth0.IPConfigs[0].IP.String()
	} else {
		for _, iface := range result.Interfaces {
			if len(iface.IPConfigs) > 0 && iface.IPConfigs[0].IP != nil {
				ip = iface.IPConfigs[0].IP.String()
				break
			}
		}
	}
	if ip == "" {
		_ = r.cniClient.Remove(ctx, nsName, nsPath)
		_ = netns.DeleteNamed(nsName)
		return nil, fmt.Errorf("no IP allocated for CNI netns %s", nsName)
	}

	return &pooledNetns{
		Name: nsName,
		Path: nsPath,
		IP:   ip,
	}, nil
}

func (r *Runtime) destroyCNINamespace(ns *pooledNetns) {
	if ns == nil {
		return
	}
	ctx := namespaces.WithNamespace(context.Background(), r.cfg.Namespace)
	if r.cniClient != nil {
		_ = r.cniClient.Remove(ctx, ns.Name, ns.Path)
	}
	if r.deleteNetns != nil {
		_ = r.deleteNetns(ns.Name)
	} else {
		_ = netns.DeleteNamed(ns.Name)
	}
}

// sweepOrphanNetns removes named network namespaces under dir whose name has
// prefix and is not present in keep. It is best-effort: failures for individual
// namespaces are ignored by destroyCNINamespace and do not abort the sweep.
// It returns the number of namespaces it attempted to remove.
func (r *Runtime) sweepOrphanNetns(dir, prefix string, keep map[string]bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.logger.Warn("failed to list network namespaces for orphan sweep", "dir", dir, "error", err)
		return 0
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || keep[name] {
			continue
		}
		r.destroyCNINamespace(&pooledNetns{Name: name, Path: filepath.Join(dir, name)})
		r.logger.Info("removed orphaned network namespace", "ns", name)
		removed++
	}
	return removed
}

// takeContainer atomically removes and returns the metadata for instanceID.
// The caller that receives a non-nil value owns the sandbox teardown.
func (r *Runtime) takeContainer(instanceID uint64) *ContainerMetadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, ok := r.containers[instanceID]
	if !ok {
		return nil
	}
	delete(r.containers, instanceID)
	return meta
}

// teardownSandbox reclaims every host-side resource for one sandbox. It is
// idempotent and must never return before releasing the network namespace and
// host port, even when the task or container record is already gone. It reports
// whether the containerd task and container were deleted cleanly.
func (r *Runtime) teardownSandbox(ctx context.Context, meta *ContainerMetadata) bool {
	if meta == nil {
		return true
	}

	nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)

	containerID := ""
	if meta.Container != nil {
		containerID = meta.Container.ID()
	}

	ok := true
	if meta.Task != nil {
		_ = meta.Task.Kill(nsCtx, syscall.SIGKILL, containerd.WithKillAll)
		if _, err := meta.Task.Delete(nsCtx, containerd.WithProcessKill); err != nil {
			r.logger.Warn("failed to delete containerd task", "container_id", containerID, "error", err)
			ok = false
		}
	}
	if meta.Container != nil {
		if err := meta.Container.Delete(nsCtx, containerd.WithSnapshotCleanup); err != nil {
			r.logger.Warn("failed to delete containerd container", "container_id", containerID, "error", err)
			ok = false
		}
	}

	if !r.cfg.UsePool {
		if meta.NetNs != "" && r.cniClient != nil {
			if err := r.cniClient.Remove(nsCtx, containerID, meta.NetNs); err != nil {
				r.logger.Warn("failed to remove CNI network for container", "container_id", containerID, "error", err)
			}
		}
	} else if meta.PooledNS != nil {
		if ok {
			r.returnNetnsToPool(meta.PooledNS)
		} else {
			// The task or container could not be deleted. Destroy the namespace
			// instead of abandoning it so it cannot accumulate, then refill the
			// pool with a fresh namespace.
			r.logger.Warn("destroying pooled netns after failed teardown", "ns", meta.PooledNS.Name, "container_id", containerID)
			r.destroyCNINamespace(meta.PooledNS)
			r.triggerCNIPoolReplenish()
		}
	}

	if ok {
		r.returnHostPort(meta.HostPort)
	} else {
		r.logger.Warn("quarantining host port after failed teardown", "port", meta.HostPort, "container_id", containerID)
		r.triggerPortReplenish()
	}
	return ok
}

// Prepare pulls the image if it is not already present.
func (r *Runtime) Prepare(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	imageRef := function.GetRuntime().GetImage()
	if imageRef == "" {
		return nil, fmt.Errorf("function runtime image is required")
	}
	nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)
	if _, err := r.resolveImage(nsCtx, imageRef); err != nil {
		return nil, err
	}
	return &core.PreparedArtifact{
		FunctionId: function.GetFunctionId(),
		Image:      imageRef,
	}, nil
}

// HasImage checks if the image exists in containerd.
func (r *Runtime) HasImage(ctx context.Context, imageRef string) (bool, error) {
	nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)
	_, err := r.client.ImageService().Get(nsCtx, imageRef)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// ensureImage checks if image is cached, otherwise pulls it.
// Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/image_manager.go -> GetImage
func (r *Runtime) ensureImage(ctx context.Context, imageRef string) (containerd.Image, error) {
	if val, ok := r.images.Load(imageRef); ok {
		return val.(containerd.Image), nil
	}
	if image, err := r.client.GetImage(ctx, imageRef); err == nil {
		r.images.Store(imageRef, image)
		return image, nil
	}

	// Pull from registry (Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/iface.go -> FetchImage)
	r.logger.Info("pulling image", "image", imageRef)
	image, err := r.client.Pull(ctx, imageRef, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}

	r.images.Store(imageRef, image)
	return image, nil
}

func (r *Runtime) resolveImage(ctx context.Context, imageRef string) (containerd.Image, error) {
	if r.cfg.SingleFlightPull {
		return r.ensureImageSingleFlight(ctx, imageRef)
	}
	return r.ensureImage(ctx, imageRef)
}

func (r *Runtime) ensureImageSingleFlight(ctx context.Context, imageRef string) (containerd.Image, error) {
	r.pullJobsMu.Lock()
	if val, ok := r.images.Load(imageRef); ok {
		r.pullJobsMu.Unlock()
		return val.(containerd.Image), nil
	}

	job, exists := r.pullJobs[imageRef]
	if exists {
		r.pullJobsMu.Unlock()
		job.wg.Wait()
		return job.img, job.err
	}

	job = &pullRequest{}
	job.wg.Add(1)
	r.pullJobs[imageRef] = job
	r.pullJobsMu.Unlock()

	img, err := r.ensureImage(ctx, imageRef)

	r.pullJobsMu.Lock()
	job.img = img
	job.err = err
	job.wg.Done()
	delete(r.pullJobs, imageRef)
	r.pullJobsMu.Unlock()

	return img, err
}

// Start creates a sandbox container and starts the task.
// Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/runtime.go -> CreateSandbox
func (r *Runtime) Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	startedAt := time.Now()
	logStep := func(step string) {
		r.logger.Debug("containerd_start_step", "instance_id", req.GetInstanceId(), "step", step, "elapsed_ms", time.Since(startedAt).Milliseconds())
	}
	function := req.GetFunction()
	if function == nil {
		return nil, fmt.Errorf("function is required")
	}
	instanceID := req.GetInstanceId()
	if instanceID == 0 {
		return nil, fmt.Errorf("instance_id is required")
	}

	imageRef := function.GetRuntime().GetImage()
	if imageRef == "" {
		if req.Artifact != nil && req.Artifact.GetImage() != "" {
			imageRef = req.Artifact.GetImage()
		}
	}
	if imageRef == "" {
		return nil, fmt.Errorf("function runtime image is required")
	}

	nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)
	image, err := r.resolveImage(nsCtx, imageRef)
	if err != nil {
		return nil, err
	}
	logStep("resolve_image")
	if err := r.waitForAsyncTeardown(ctx); err != nil {
		return nil, fmt.Errorf("waiting for async teardown: %w", err)
	}
	logStep("wait_async_teardown")

	containerName := fmt.Sprintf("hyperfaas-%d", instanceID)

	hostPort, err := r.borrowHostPort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate free port: %w", err)
	}
	logStep("borrow_host_port")
	releasePort := true
	defer func() {
		if releasePort {
			r.returnHostPort(hostPort)
		}
	}()

	var netnsInfo *pooledNetns
	if r.cfg.UsePool && r.pool != nil {
		select {
		case netnsInfo = <-r.pool:
			r.logger.Debug("borrowed CNI netns from pool", "ns", netnsInfo.Name)
		default:
			r.logger.Info("CNI netns pool empty, creating namespace on the fly")
			netnsInfo, err = r.createCNINamespace(instanceID)
			if err != nil {
				return nil, err
			}
		}
	}

	controllerAddr := r.controllerAddress(netnsInfo, nil)
	env := []string{
		"INSTANCE_ID=" + strconv.FormatUint(instanceID, 10),
		"FUNCTION_ID=" + strconv.FormatUint(function.GetFunctionId(), 10),
		"FUNCTION_PORT=" + strconv.Itoa(functionPort),
		"CONTROLLER_ADDRESS=" + controllerAddr,
	}
	for key, value := range function.GetRuntime().GetEnv() {
		env = append(env, key+"="+value)
	}

	// Spec options are consumed when NewTask creates the OCI runtime bundle.
	var specOpts []oci.SpecOpts
	specOpts = append(specOpts, oci.WithImageConfig(image), oci.WithEnv(env))
	if r.cfg.UsePool && netnsInfo != nil {
		specOpts = append(specOpts, oci.WithLinuxNamespace(specs.LinuxNamespace{
			Type: specs.NetworkNamespace,
			Path: netnsInfo.Path,
		}))
	}
	if resources := function.GetRuntime().GetResources(); resources != nil {
		if resources.GetMemoryBytes() > 0 {
			specOpts = append(specOpts, oci.WithMemoryLimit(resources.GetMemoryBytes()))
		}
		if resources.GetCpuUnits() > 0 {
			quota := int64(100000 * resources.GetCpuUnits() / 1000)
			specOpts = append(specOpts, oci.WithCPUCFS(quota, 100000))
		}
	}

	// Create Container (Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/iface.go -> CreateContainer)
	container, err := r.client.NewContainer(nsCtx, containerName,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(containerName, image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		if r.cfg.UsePool && netnsInfo != nil {
			r.returnNetnsToPool(netnsInfo)
		}
		return nil, fmt.Errorf("failed to create containerd container: %w", err)
	}
	logStep("new_container")

	// Create and CNI configure container task (Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/iface.go -> StartContainer)
	task, err := container.NewTask(nsCtx, cio.NewCreator())
	if err != nil {
		_ = container.Delete(nsCtx, containerd.WithSnapshotCleanup)
		if r.cfg.UsePool && netnsInfo != nil {
			r.returnNetnsToPool(netnsInfo)
		}
		return nil, fmt.Errorf("failed to create containerd task: %w", err)
	}
	logStep("new_task")

	var ip string
	var netns string
	var result *cni.Result

	if r.cfg.UsePool && netnsInfo != nil {
		ip = netnsInfo.IP
		netns = netnsInfo.Path
	} else {
		netns = fmt.Sprintf("/proc/%v/ns/net", task.Pid())
		var errSetup error
		result, errSetup = r.cniClient.Setup(nsCtx, container.ID(), netns)
		if errSetup != nil {
			_, _ = task.Delete(nsCtx, containerd.WithProcessKill)
			_ = container.Delete(nsCtx, containerd.WithSnapshotCleanup)
			return nil, fmt.Errorf("failed to setup CNI network: %w", errSetup)
		}

		if eth0, ok := result.Interfaces["eth0"]; ok && len(eth0.IPConfigs) > 0 && eth0.IPConfigs[0].IP != nil {
			ip = eth0.IPConfigs[0].IP.String()
		} else {
			for _, iface := range result.Interfaces {
				if len(iface.IPConfigs) > 0 && iface.IPConfigs[0].IP != nil {
					ip = iface.IPConfigs[0].IP.String()
					break
				}
			}
		}
		if ip == "" {
			_ = r.cniClient.Remove(nsCtx, container.ID(), netns)
			_, _ = task.Delete(nsCtx, containerd.WithProcessKill)
			_ = container.Delete(nsCtx, containerd.WithSnapshotCleanup)
			return nil, fmt.Errorf("failed to retrieve guest IP from CNI result")
		}
	}
	logStep("cni_setup")

	// Start task
	err = task.Start(nsCtx)
	if err != nil {
		if !r.cfg.UsePool {
			_ = r.cniClient.Remove(nsCtx, container.ID(), netns)
		} else if netnsInfo != nil {
			r.returnNetnsToPool(netnsInfo)
		}
		_, _ = task.Delete(nsCtx, containerd.WithProcessKill)
		_ = container.Delete(nsCtx, containerd.WithSnapshotCleanup)
		return nil, fmt.Errorf("failed to start containerd task: %w", err)
	}
	logStep("task_start")

	// Configure IP tables rules (Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/runtime.go -> CreateSandbox)
	r.addIptablesRules(hostPort, ip, functionPort)
	logStep("iptables")
	releasePort = false

	r.mu.Lock()
	r.containers[instanceID] = &ContainerMetadata{
		Container: container,
		Task:      task,
		HostPort:  hostPort,
		GuestIP:   ip,
		NetNs:     netns,
		PooledNS:  netnsInfo,
	}
	r.mu.Unlock()

	protocol := function.GetRuntime().GetProtocol()
	if protocol == "" {
		protocol = "grpc"
	}

	hostIP := localHostIP()
	if hostIP == "" {
		hostIP = "127.0.0.1"
	}

	return &core.InstanceState{
		InstanceId: instanceID,
		FunctionId: function.GetFunctionId(),
		WorkerId:   req.GetWorkerId(),
		Address:    net.JoinHostPort(hostIP, strconv.Itoa(hostPort)),
		Protocol:   protocol,
		Ready:      false,
		StartedAt:  timestamppb.Now(),
	}, nil
}

// Stop shuts down the task and deletes the container.
// Adapted from Dirigent: forks/dirigent/internal/worker_node/sandbox/containerd/runtime.go -> DeleteSandbox
func (r *Runtime) Stop(ctx context.Context, instanceID uint64) error {
	meta := r.takeContainer(instanceID)
	if meta == nil {
		// The sandbox has already been reclaimed, typically by the lifecycle
		// watcher when its task exited. Treat stop as idempotent: the previous
		// behaviour returned an error here and skipped every cleanup step,
		// leaving the network namespace, container record and host port behind.
		r.logger.Debug("sandbox already stopped", "instance_id", instanceID)
		return nil
	}

	// Remove iptables rules synchronously so the port is freed immediately
	// (Adapted from Dirigent: .../iptables_manager.go -> DeleteRules)
	r.deleteIptablesRules(meta.HostPort, meta.GuestIP, functionPort)

	if r.cfg.AsyncTeardown {
		r.beginAsyncTeardown()
		go func() {
			defer r.endAsyncTeardown()
			teardownCtx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
			defer cancel()
			r.teardownSandbox(teardownCtx, meta)
		}()
		return nil
	}

	r.teardownSandbox(ctx, meta)
	return nil
}

// Stats returns resource usage metrics for the task.
func (r *Runtime) Stats(ctx context.Context, instanceID uint64) (*core.ResourceUsage, error) {
	r.mu.RLock()
	meta, ok := r.containers[instanceID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("container not found for instance ID %d", instanceID)
	}

	nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)
	metrics, err := meta.Task.Metrics(nsCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task metrics: %w", err)
	}

	usage := &core.ResourceUsage{}
	if metrics.Data != nil {
		stats, err := typeurl.UnmarshalAny(metrics.Data)
		if err == nil {
			switch s := stats.(type) {
			case *v1.Metrics:
				if s.Memory != nil && s.Memory.Usage != nil {
					usage.MemoryBytes = s.Memory.Usage.Usage
				}
				if s.CPU != nil && s.CPU.Usage != nil {
					usage.CpuUnits = s.CPU.Usage.Total
				}
			}
		}
	}
	return usage, nil
}

// WatchLifecycle monitors the container for crashes or exits.
func (r *Runtime) WatchLifecycle(ctx context.Context, instanceID uint64) (<-chan runtime.LifecycleEvent, error) {
	r.mu.RLock()
	meta, ok := r.containers[instanceID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("container not found for instance ID %d", instanceID)
	}

	out := make(chan runtime.LifecycleEvent, 1)
	go func() {
		defer close(out)

		nsCtx := namespaces.WithNamespace(ctx, r.cfg.Namespace)
		statusChan, err := meta.Task.Wait(nsCtx)
		if err != nil {
			r.logger.Error("failed to wait on task", "instance_id", instanceID, "error", err)
			return
		}

		select {
		case status := <-statusChan:
			r.logger.Info("containerd task exited", "instance_id", instanceID, "exit_code", status.ExitCode())
			if status.ExitCode() == 0 {
				out <- runtime.LifecycleExit
			} else {
				out <- runtime.LifecycleCrash
			}
		case <-ctx.Done():
			// The watcher was canceled without an observed exit; leave the
			// metadata in place so Stop can still drive teardown.
			return
		}

		// Reclaim all host-side state here instead of only dropping the map
		// entry. Previously an exited task left its named network namespace,
		// containerd container record, iptables rules and host port behind, and
		// a later Stop() saw a missing entry and skipped cleanup entirely.
		if r.takeContainer(instanceID) != meta {
			// Stop already took ownership of the teardown.
			return
		}
		r.deleteIptablesRules(meta.HostPort, meta.GuestIP, functionPort)
		r.beginAsyncTeardown()
		defer r.endAsyncTeardown()
		teardownCtx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
		defer cancel()
		r.teardownSandbox(teardownCtx, meta)
	}()
	return out, nil
}

func (r *Runtime) getFreePort() (int, error) {
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

// Adapted from Dirigent: forks/dirigent/internal/worker_node/managers/iptables_manager.go -> AddRules
func (r *Runtime) addIptablesRules(sourcePort int, destIP string, destPort int) {
	err := r.ipt.Append(
		"nat",
		"PREROUTING",
		"-p", "tcp", "--dport", strconv.Itoa(sourcePort), "-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", destIP, destPort),
	)
	if err != nil {
		r.logger.Error("error adding PREROUTING rule", "port", sourcePort, "dest", destIP, "error", err)
	}

	err = r.ipt.Append(
		"nat",
		"OUTPUT",
		"-p", "tcp", "-o", "lo", "--dport", strconv.Itoa(sourcePort), "-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", destIP, destPort),
	)
	if err != nil {
		r.logger.Error("error adding OUTPUT rule", "port", sourcePort, "dest", destIP, "error", err)
	}

	err = r.ipt.AppendUnique(
		"nat",
		"POSTROUTING",
		"-j", "MASQUERADE",
	)
	if err != nil {
		r.logger.Error("error adding POSTROUTING MASQUERADE", "error", err)
	}

	err = exec.Command("iptables", "-P", "FORWARD", "ACCEPT").Run()
	if err != nil {
		r.logger.Error("error changing forwarding policy", "error", err)
	}
}

// Adapted from Dirigent: forks/dirigent/internal/worker_node/managers/iptables_manager.go -> DeleteRules
func (r *Runtime) deleteIptablesRules(sourcePort int, destIP string, destPort int) {
	if r.ipt == nil {
		return
	}
	err := r.ipt.Delete(
		"nat",
		"PREROUTING",
		"-p", "tcp", "--dport", strconv.Itoa(sourcePort), "-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", destIP, destPort),
	)
	if err != nil {
		r.logger.Warn("error deleting PREROUTING rule", "port", sourcePort, "dest", destIP, "error", err)
	}

	err = r.ipt.Delete(
		"nat",
		"OUTPUT",
		"-p", "tcp", "-o", "lo", "--dport", strconv.Itoa(sourcePort), "-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", destIP, destPort),
	)
	if err != nil {
		r.logger.Warn("error deleting OUTPUT rule", "port", sourcePort, "dest", destIP, "error", err)
	}
}

func localHostIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
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

func (r *Runtime) controllerAddress(netnsInfo *pooledNetns, result *cni.Result) string {
	if r.cfg.UsePool && netnsInfo != nil {
		return net.JoinHostPort("100.23.0.1", "50052")
	}
	gatewayIP := "100.23.0.1"
	if gw := getGatewayIP(result); gw != "" {
		gatewayIP = gw
	}
	_, controllerPort, err := net.SplitHostPort(r.cfg.WorkerListenAddress)
	if err != nil {
		return net.JoinHostPort(gatewayIP, "50052")
	}
	return net.JoinHostPort(gatewayIP, controllerPort)
}

func getGatewayIP(result *cni.Result) string {
	if result == nil {
		return ""
	}
	for _, iface := range result.Interfaces {
		for _, ipConf := range iface.IPConfigs {
			if ipConf.Gateway != nil {
				return ipConf.Gateway.String()
			}
		}
	}
	return ""
}
