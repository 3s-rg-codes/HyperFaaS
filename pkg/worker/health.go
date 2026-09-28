package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"hyperfaas-ideal-arch/pkg/core"
)

const cpuUnitsPerCore = 1000
const defaultHealthInterval = time.Second

// ProcHealthService reports host-level worker resources from /proc.
//
// The scheduler consumes WorkerState as a capacity hint, not as an accounting
// ledger. This service therefore samples inexpensive host counters and keeps
// only the previous CPU sample needed to calculate utilization between ticks.
// It does not store historical metrics or per-container stats.
type ProcHealthService struct {
	cfg     WorkerConfig
	sandbox SandboxService

	mu        sync.Mutex
	lastTotal uint64
	lastIdle  uint64

	overrideMu   sync.Mutex
	loadOverride *float64
}

func NewProcHealthService(cfg WorkerConfig, sandbox SandboxService) *ProcHealthService {
	return &ProcHealthService{cfg: cfg, sandbox: sandbox}
}

// CurrentState builds a diagnostic full snapshot. It is deliberately ungated
// and computes every field; the projection-gated WatchState path does not use
// it.
func (s *ProcHealthService) CurrentState(ctx context.Context) (*core.WorkerState, error) {
	return s.currentState(ctx, FullStateProjection())
}

// currentState builds one WorkerState for a projection. Gated fields are only
// computed when requested, so an unrequested signal costs nothing at the
// source (no /proc read, no CachedImages allocation).
func (s *ProcHealthService) currentState(ctx context.Context, projection StateProjection) (*core.WorkerState, error) {
	var loadNorm float64
	var err error
	if projection.LoadAverageNorm {
		loadNorm, err = s.loadAverageNorm()
		if err != nil {
			return nil, err
		}
	}
	if s.sandbox == nil {
		return &core.WorkerState{
			Schedulable:     true,
			Healthy:         true,
			LoadAverageNorm: loadNorm,
		}, nil
	}
	var instances []*core.InstanceState
	var sandboxRevision uint64
	if snapshotter, ok := s.sandbox.(interface {
		SandboxStateSnapshot() ([]*core.InstanceState, uint64)
	}); ok {
		instances, sandboxRevision = snapshotter.SandboxStateSnapshot()
	} else {
		instances, err = s.sandbox.ListSandboxes(ctx)
		if err != nil {
			return nil, err
		}
	}
	capacity, allocated, err := s.resources()
	if err != nil {
		return nil, err
	}
	state := &core.WorkerState{
		Schedulable:     true,
		Healthy:         true,
		Capacity:        capacity,
		Allocated:       allocated,
		Instances:       uint64(len(instances)),
		SandboxStates:   instances,
		SandboxRevision: sandboxRevision,
		LoadAverageNorm: loadNorm,
	}
	if projection.CachedImages {
		state.CachedImages = s.sandbox.CachedImages()
	}
	return state, nil
}

// SetLoadAverageNormOverride pins the CH-BL load signal. Used by DST; production
// invokers publish the sampled 1-minute load average instead.
func (s *ProcHealthService) SetLoadAverageNormOverride(value float64, clear bool) {
	s.overrideMu.Lock()
	defer s.overrideMu.Unlock()
	if clear {
		s.loadOverride = nil
		return
	}
	v := value
	s.loadOverride = &v
}

func (s *ProcHealthService) loadAverageNorm() (float64, error) {
	s.overrideMu.Lock()
	override := s.loadOverride
	s.overrideMu.Unlock()
	if override != nil {
		return *override, nil
	}
	return readLoadAverageNorm()
}

func readLoadAverageNorm() (float64, error) {
	load1, err := readLoadavg1()
	if err != nil {
		return 0, err
	}
	n := runtime.NumCPU()
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	if n <= 0 {
		n = 1
	}
	return load1 / float64(n), nil
}

func readLoadavg1() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 1 {
		return 0, errors.New("/proc/loadavg is empty")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func (s *ProcHealthService) WatchState(ctx context.Context, projection StateProjection) (<-chan *core.WorkerState, <-chan error) {
	// Buffer exactly one state update. If the gRPC sender is slower than the
	// sampler, sendState replaces the queued value with the newest one. The leaf
	// only needs the latest resource picture for placement, so retaining a backlog
	// would waste memory and make scheduling decisions older.
	updates := make(chan *core.WorkerState, 1)
	errs := make(chan error, 1)
	go func() {
		defer close(updates)
		defer close(errs)
		s.sendState(ctx, updates, errs, projection)
		interval := s.cfg.Stats.MetricsInterval
		if interval <= 0 {
			interval = defaultHealthInterval
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sendState(ctx, updates, errs, projection)
			}
		}
	}()
	return updates, errs
}

func (s *ProcHealthService) sendState(ctx context.Context, updates chan *core.WorkerState, errs chan<- error, projection StateProjection) {
	state, err := s.currentState(ctx, projection)
	if err != nil {
		select {
		case errs <- err:
		default:
		}
		return
	}
	select {
	case updates <- state:
	default:
		// Coalesce to latest state. This intentionally drops stale samples.
		<-updates
		updates <- state
	}
}

func (s *ProcHealthService) resources() (*core.ResourceSpec, *core.ResourceUsage, error) {
	var capacityCPU uint64
	if s.cfg.Stats.BudgetCPU > 0 {
		capacityCPU = uint64(s.cfg.Stats.BudgetCPU * cpuUnitsPerCore)
	}
	if capacityCPU == 0 {
		// BudgetCPU is the schedulable CPU budget. If it is not configured, use
		// host CPU count so resource-aware scheduling still has a usable capacity.
		capacityCPU = uint64(runtime.NumCPU() * cpuUnitsPerCore)
	}
	var capacityMemory uint64
	if s.cfg.Stats.BudgetMemory > 0 {
		capacityMemory = uint64(s.cfg.Stats.BudgetMemory)
	}
	memoryTotal, memoryAvailable, err := readMeminfo()
	if err != nil {
		return nil, nil, err
	}
	if capacityMemory == 0 {
		// BudgetMemory limits schedulable memory. Falling back to MemTotal keeps
		// the worker useful in tests and local configs that omit budgets.
		capacityMemory = memoryTotal
	}
	allocatedMemory := memoryTotal - memoryAvailable
	if allocatedMemory > capacityMemory {
		allocatedMemory = capacityMemory
	}
	diskTotal, diskUsed, err := diskUsage("/")
	if err != nil {
		return nil, nil, err
	}
	return &core.ResourceSpec{
		CpuUnits:    capacityCPU,
		MemoryBytes: capacityMemory,
		DiskBytes:   diskTotal,
	}, &core.ResourceUsage{
		CpuUnits:    s.cpuAllocated(capacityCPU),
		MemoryBytes: allocatedMemory,
		DiskBytes:   diskUsed,
	}, nil
}

func (s *ProcHealthService) cpuAllocated(capacity uint64) uint64 {
	total, idle, err := readCPUStat()
	if err != nil || total == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastTotal == 0 || total <= s.lastTotal || idle < s.lastIdle {
		// CPU usage is a rate, so the first sample only seeds the baseline. A
		// counter reset is treated the same way and reports zero for this sample.
		s.lastTotal = total
		s.lastIdle = idle
		return 0
	}
	totalDelta := total - s.lastTotal
	idleDelta := idle - s.lastIdle
	s.lastTotal = total
	s.lastIdle = idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	// Scale host-wide busy time into the same cpu_units domain as capacity.
	return capacity * (totalDelta - idleDelta) / totalDelta
}

func readCPUStat() (uint64, uint64, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() {
		return 0, 0, errors.New("/proc/stat is empty")
	}
	fields := strings.Fields(s.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat cpu line")
	}
	var total uint64
	var idle uint64
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += v
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return total, idle, nil
}

func readMeminfo() (uint64, uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var total, available uint64
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
		if total > 0 && available > 0 {
			return total, available, nil
		}
	}
	if err := s.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, errors.New("missing memory totals in /proc/meminfo")
}

func diskUsage(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	return total, total - free, nil
}
