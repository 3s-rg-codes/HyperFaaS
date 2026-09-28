package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/worker/runtime"
	containerdruntime "hyperfaas-ideal-arch/pkg/worker/runtime/containerd"
	dockerruntime "hyperfaas-ideal-arch/pkg/worker/runtime/docker"
	fakeruntime "hyperfaas-ideal-arch/pkg/worker/runtime/fake"
	firecrackerruntime "hyperfaas-ideal-arch/pkg/worker/runtime/firecracker"
	runcruntime "hyperfaas-ideal-arch/pkg/worker/runtime/runc"
)

const defaultSandboxStartTimeout = 60 * time.Second

type sandboxRecord struct {
	state *core.InstanceState
}

// SandboxRemoval is a worker-local notification that one sandbox left the
// sandbox map. Revision is the sandbox revision after the removal and lets the
// leaf reject an event older than the instance's create revision.
type SandboxRemoval struct {
	InstanceID uint64
	FunctionID uint64
	Revision   uint64
}

// Sandbox implements SandboxService using a pluggable runtime backend.
type Sandbox struct {
	runtime      runtime.Runtime
	readySignals *ReadySignals
	logger       *slog.Logger
	startTimeout time.Duration

	mu            sync.RWMutex
	instances     map[uint64]*sandboxRecord
	cachedImages  map[string]*core.CachedImage // keyed by image ref
	stateRevision uint64

	// removals is a best-effort broadcast of SandboxRemoval events to the
	// owning leaf. Slow subscribers drop events; the periodic, revisioned
	// SandboxStateSnapshot is the anti-entropy backstop.
	removalsMu   sync.Mutex
	removalsSubs map[int]chan SandboxRemoval
	removalsNext int
}

func NewSandbox(cfg WorkerConfig, logger *slog.Logger) (*Sandbox, error) {
	rt, err := newRuntime(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &Sandbox{
		runtime:      rt,
		readySignals: NewReadySignals(),
		logger:       logger,
		startTimeout: defaultSandboxStartTimeout,
		instances:    make(map[uint64]*sandboxRecord),
		cachedImages: make(map[string]*core.CachedImage),
		removalsSubs: make(map[int]chan SandboxRemoval),
	}, nil
}

func newRuntime(cfg WorkerConfig, logger *slog.Logger) (runtime.Runtime, error) {
	switch cfg.Runtime.Type {
	case "docker":
		return dockerruntime.New(dockerruntime.Config{
			AutoRemove:          cfg.Runtime.Docker.AutoRemove,
			NetworkName:         cfg.Runtime.Docker.NetworkName,
			WorkerListenAddress: cfg.Server.ListenAddress,
		}, logger)
	case "firecracker":
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
	case "runc":
		return runcruntime.New(runcruntime.Config{
			WorkDir:             cfg.Runtime.RunC.WorkDir,
			WorkerListenAddress: cfg.Server.ListenAddress,
			NetworkIsolation:    cfg.Runtime.RunC.NetworkIsolation,
			NetworkMode:         cfg.Runtime.RunC.NetworkMode,
			UsePool:             cfg.Runtime.RunC.UsePool,
			PoolSize:            cfg.Runtime.RunC.PoolSize,
			ArtifactsBucket:     cfg.ArtifactsBucket,
		}, logger)
	case "containerd":
		return containerdruntime.New(containerdruntime.Config{
			CRIPath:             cfg.Runtime.Containerd.CRIPath,
			CNIConfigPath:       cfg.Runtime.Containerd.CNIConfigPath,
			Namespace:           cfg.Runtime.Containerd.Namespace,
			PrefetchImage:       cfg.Runtime.Containerd.PrefetchImage,
			WorkerListenAddress: cfg.Server.ListenAddress,
			UsePool:             cfg.Runtime.Containerd.UsePool,
			PoolSize:            cfg.Runtime.Containerd.PoolSize,
			AsyncTeardown:       cfg.Runtime.Containerd.AsyncTeardown,
			SingleFlightPull:    cfg.Runtime.Containerd.SingleFlightPull,
		}, logger)
	case "fake":
		return fakeruntime.New(fakeruntime.Config{
			SimulateSandboxStartLatency: cfg.Runtime.Fake.SimulateSandboxStartLatency,
		}, logger), nil
	default:
		return nil, fmt.Errorf("unsupported runtime type %q", cfg.Runtime.Type)
	}
}

func (s *Sandbox) PrepareImage(ctx context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	artifact, err := s.runtime.Prepare(ctx, function)
	if err != nil {
		return nil, err
	}
	s.recordCachedImage(function, artifact)
	return artifact, nil
}

func (s *Sandbox) HasImage(ctx context.Context, image string) (bool, error) {
	if image = strings.TrimSpace(image); image != "" {
		s.mu.RLock()
		_, ok := s.cachedImages[image]
		if !ok {
			for _, cached := range s.cachedImages {
				if cached != nil && (cached.GetDigest() == image || strings.HasSuffix(image, "@"+cached.GetDigest())) {
					ok = true
					break
				}
			}
		}
		s.mu.RUnlock()
		if ok {
			return true, nil
		}
	}
	return s.runtime.HasImage(ctx, image)
}

func (s *Sandbox) CachedImages() []*core.CachedImage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*core.CachedImage, 0, len(s.cachedImages))
	for _, img := range s.cachedImages {
		out = append(out, proto.Clone(img).(*core.CachedImage))
	}
	return out
}

func (s *Sandbox) recordCachedImage(function *core.FunctionSpec, artifact *core.PreparedArtifact) {
	ref := ""
	if artifact != nil {
		ref = strings.TrimSpace(artifact.GetImage())
	}
	if ref == "" && function != nil && function.GetRuntime() != nil {
		ref = strings.TrimSpace(function.GetRuntime().GetImage())
	}
	if ref == "" {
		return
	}
	digest := digestFromImageRef(ref)
	entry := &core.CachedImage{
		Image:    ref,
		Digest:   digest,
		CachedAt: timestamppb.Now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedImages == nil {
		s.cachedImages = make(map[string]*core.CachedImage)
	}
	if prev, ok := s.cachedImages[ref]; ok && prev != nil {
		entry.SizeBytes = prev.GetSizeBytes()
		if entry.Digest == "" {
			entry.Digest = prev.GetDigest()
		}
	}
	s.cachedImages[ref] = entry
}

func digestFromImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "sha256:") {
		return ref
	}
	if i := strings.LastIndex(ref, "@"); i >= 0 && i+1 < len(ref) {
		d := ref[i+1:]
		if strings.HasPrefix(d, "sha256:") {
			return d
		}
	}
	return ""
}

func (s *Sandbox) CreateSandbox(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	instanceID := req.GetInstanceId()
	if instanceID == 0 {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	function := req.GetFunction()
	functionID := uint64(0)
	image := ""
	if function != nil {
		functionID = function.GetFunctionId()
		if function.GetRuntime() != nil {
			image = function.GetRuntime().GetImage()
		}
	}

	s.readySignals.AddInstance(instanceID)

	startCtx, cancel := context.WithTimeout(ctx, s.startTimeout)
	defer cancel()
	startedAt := time.Now()
	s.logger.Info("runtime_start_begin",
		"function_id", functionID,
		"instance_id", instanceID,
		"worker_id", req.GetWorkerId(),
		"image", image,
	)

	state, err := s.runtime.Start(startCtx, req)
	if err != nil {
		s.logger.Warn("sandbox start failed",
			"function_id", functionID,
			"instance_id", instanceID,
			"worker_id", req.GetWorkerId(),
			"startup_ms", time.Since(startedAt).Milliseconds(),
			"error", err,
		)
		return nil, err
	}
	s.logger.Info("sandbox container started",
		"function_id", functionID,
		"instance_id", instanceID,
		"worker_id", req.GetWorkerId(),
		"address", state.GetAddress(),
		"startup_ms", time.Since(startedAt).Milliseconds(),
	)

	s.mu.Lock()
	s.stateRevision++
	state.WorkerStateRevision = s.stateRevision
	s.instances[instanceID] = &sandboxRecord{state: state}
	s.mu.Unlock()

	if watcher, ok := s.runtime.(runtime.LifecycleWatcher); ok {
		go s.watchLifecycle(instanceID, watcher)
	}

	if state.GetReady() {
		s.readySignals.SignalReady(instanceID)
		if err := pingTCP(state.GetAddress(), 2*time.Second); err != nil {
			_ = s.runtime.Stop(context.Background(), instanceID)
			s.removeInstance(instanceID)
			return nil, status.Errorf(codes.Unavailable, "sandbox host port not reachable: %v", err)
		}
		s.logger.Info("sandbox_ready",
			"function_id", functionID,
			"instance_id", instanceID,
			"worker_id", req.GetWorkerId(),
			"address", state.GetAddress(),
			"cold_start_ms", time.Since(startedAt).Milliseconds(),
		)
		return cloneInstanceState(state), nil
	}

	waitDone := make(chan struct{})
	go func() {
		s.readySignals.WaitReady(instanceID)
		close(waitDone)
	}()

	select {
	case <-waitDone:
		if err := pingTCP(state.GetAddress(), 2*time.Second); err != nil {
			_ = s.runtime.Stop(context.Background(), instanceID)
			s.removeInstance(instanceID)
			s.logger.Warn("sandbox ready check failed",
				"function_id", functionID,
				"instance_id", instanceID,
				"worker_id", req.GetWorkerId(),
				"startup_ms", time.Since(startedAt).Milliseconds(),
				"error", err,
			)
			return nil, status.Errorf(codes.Unavailable, "sandbox host port not reachable: %v", err)
		}
		s.mu.Lock()
		state.Ready = true
		if rec, ok := s.instances[instanceID]; ok {
			rec.state.Ready = true
		}
		s.mu.Unlock()
		s.logger.Info("sandbox_ready",
			"function_id", functionID,
			"instance_id", instanceID,
			"worker_id", req.GetWorkerId(),
			"address", state.GetAddress(),
			"cold_start_ms", time.Since(startedAt).Milliseconds(),
		)
		return cloneInstanceState(state), nil
	case <-startCtx.Done():
		_ = s.runtime.Stop(context.Background(), instanceID)
		s.removeInstance(instanceID)
		s.logger.Warn("sandbox ready timeout",
			"function_id", functionID,
			"instance_id", instanceID,
			"worker_id", req.GetWorkerId(),
			"startup_ms", time.Since(startedAt).Milliseconds(),
		)
		return nil, status.Error(codes.DeadlineExceeded, "sandbox did not become ready in time")
	case <-ctx.Done():
		s.logger.Warn("sandbox start canceled",
			"function_id", functionID,
			"instance_id", instanceID,
			"worker_id", req.GetWorkerId(),
			"startup_ms", time.Since(startedAt).Milliseconds(),
			"error", ctx.Err(),
		)
		return nil, ctx.Err()
	}
}

func (s *Sandbox) StopSandbox(ctx context.Context, instanceID uint64) error {
	startedAt := time.Now()
	if err := s.runtime.Stop(ctx, instanceID); err != nil {
		s.logger.Warn("sandbox stop failed", "instance_id", instanceID, "stop_ms", time.Since(startedAt).Milliseconds(), "error", err)
		return err
	}
	s.removeInstance(instanceID)
	s.logger.Info("sandbox stopped", "instance_id", instanceID, "stop_ms", time.Since(startedAt).Milliseconds())
	return nil
}

// ListSandboxes returns every sandbox with per-sandbox resource usage.
//
// This is very inefficient: it performs one runtime Stats call (a Docker API
// round trip) per ready sandbox, sequentially, so it is O(instances) blocking
// calls. It is intended only for tests, debugging, and one-off inspection.
// It must never be used on a hot path. Hot paths use SandboxStateSnapshot plus
// the WatchSandboxEvents removal stream.
func (s *Sandbox) ListSandboxes(ctx context.Context) ([]*core.InstanceState, error) {
	out, _ := s.SandboxStateSnapshot()

	for _, inst := range out {
		if inst.Ready && !inst.Stopping {
			usage, err := s.runtime.Stats(ctx, inst.InstanceId)
			if err == nil && usage != nil {
				inst.Usage = usage
			} else {
				s.logger.Debug("failed to query sandbox stats", "instance_id", inst.InstanceId, "error", err)
			}
		}
	}
	return out, nil
}

// SandboxStateSnapshot returns an atomic, fast identity snapshot. Runtime
// statistics are deliberately excluded: worker state reconciliation must not
// wait for one Docker Stats call per sandbox.
func (s *Sandbox) SandboxStateSnapshot() ([]*core.InstanceState, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*core.InstanceState, 0, len(s.instances))
	for _, record := range s.instances {
		out = append(out, cloneInstanceState(record.state))
	}
	return out, s.stateRevision
}

func (s *Sandbox) SignalReady(ctx context.Context, instanceID uint64) error {
	_ = ctx
	if instanceID == 0 {
		return status.Error(codes.InvalidArgument, "instance_id is required")
	}
	s.readySignals.SignalReady(instanceID)
	return nil
}

func (s *Sandbox) watchLifecycle(instanceID uint64, watcher runtime.LifecycleWatcher) {
	events, err := watcher.WatchLifecycle(context.Background(), instanceID)
	if err != nil {
		s.logger.Debug("lifecycle watch failed", "instance_id", instanceID, "error", err)
		return
	}
	event, ok := <-events
	if !ok {
		return
	}
	s.logger.Info("sandbox lifecycle event", "instance_id", instanceID, "event", event)
	s.removeInstanceAndNotify(instanceID)
}

func (s *Sandbox) removeInstance(instanceID uint64) {
	s.removeInstanceInternal(instanceID, false)
}

// removeInstanceAndNotify removes an instance and emits a removal event. It is
// used for exits the leaf did not initiate, such as crashes and OOM kills.
func (s *Sandbox) removeInstanceAndNotify(instanceID uint64) {
	s.removeInstanceInternal(instanceID, true)
}

func (s *Sandbox) removeInstanceInternal(instanceID uint64, notify bool) {
	s.mu.Lock()
	record, existed := s.instances[instanceID]
	var revision, functionID uint64
	if existed {
		delete(s.instances, instanceID)
		s.stateRevision++
		revision = s.stateRevision
		functionID = record.state.GetFunctionId()
	}
	s.mu.Unlock()
	if existed && notify {
		s.broadcastRemoval(SandboxRemoval{
			InstanceID: instanceID,
			FunctionID: functionID,
			Revision:   revision,
		})
	}
}

// SubscribeSandboxRemovals registers a best-effort removal subscriber. The
// returned channel is closed when ctx is done. Events are dropped for a
// subscriber that cannot keep up; the periodic SandboxStateSnapshot repairs
// any missed removal.
func (s *Sandbox) SubscribeSandboxRemovals(ctx context.Context) <-chan SandboxRemoval {
	ch := make(chan SandboxRemoval, 64)
	s.removalsMu.Lock()
	id := s.removalsNext
	s.removalsNext++
	s.removalsSubs[id] = ch
	s.removalsMu.Unlock()

	go func() {
		<-ctx.Done()
		s.removalsMu.Lock()
		if existing, ok := s.removalsSubs[id]; ok {
			delete(s.removalsSubs, id)
			close(existing)
		}
		s.removalsMu.Unlock()
	}()
	return ch
}

// broadcastRemoval fans out one removal event without blocking. The removalsMu
// lock also serializes sends against subscriber teardown so a closed channel is
// never written to.
func (s *Sandbox) broadcastRemoval(event SandboxRemoval) {
	s.removalsMu.Lock()
	defer s.removalsMu.Unlock()
	for _, ch := range s.removalsSubs {
		select {
		case ch <- event:
		default:
		}
	}
}

func cloneInstanceState(state *core.InstanceState) *core.InstanceState {
	if state == nil {
		return nil
	}
	return proto.Clone(state).(*core.InstanceState)
}

func pingTCP(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("port %s not reachable after %s", address, timeout)
}
