package stats

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mtrcs "github.com/3s-rg-codes/HyperFaaS/pkg/metrics"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type MetricsSampler struct {
	containerized bool

	logger *slog.Logger
	mu     sync.Mutex

	lastTotal    float64
	lastIdle     float64
	lastCPUUsage float64
	lastSample   time.Time
	initialized  bool

	snapshot atomic.Value
}

func NewMetricsSampler(containerized bool, logger *slog.Logger) *MetricsSampler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MetricsSampler{
		containerized: containerized,
		logger:        logger,
	}
}

func (s *MetricsSampler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	if _, err := s.Sample(); err != nil {
		s.logger.Debug("metrics sampler initial sample failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Sample(); err != nil {
				s.logger.Debug("metrics sampler tick failed", "error", err)
			}
		}
	}
}

func (s *MetricsSampler) Latest() (mtrcs.ResourceMetrics, bool) {
	value := s.snapshot.Load()
	if value == nil {
		return mtrcs.ResourceMetrics{}, false
	}
	metrics, ok := value.(mtrcs.ResourceMetrics)
	return metrics, ok
}

// Sample collects metrics and stores them as the latest snapshot.
func (s *MetricsSampler) Sample() (mtrcs.ResourceMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	metrics, err := s.sampleLocked()
	if err != nil {
		return mtrcs.ResourceMetrics{}, err
	}
	s.snapshot.Store(metrics)
	return metrics, nil
}

func (s *MetricsSampler) sampleLocked() (mtrcs.ResourceMetrics, error) {
	if s.containerized {
		return s.sampleContainerized()
	}

	return s.sampleHost()
}

func (s *MetricsSampler) sampleHost() (mtrcs.ResourceMetrics, error) {
	times, err := cpu.Times(false)
	if err != nil {
		return mtrcs.ResourceMetrics{}, err
	}
	if len(times) == 0 {
		return mtrcs.ResourceMetrics{}, nil
	}
	memStat, err := mem.VirtualMemory()
	if err != nil {
		return mtrcs.ResourceMetrics{}, err
	}

	total := totalCPUTimes(&times[0])
	idle := times[0].Idle + times[0].Iowait

	metrics := mtrcs.ResourceMetrics{}
	if memStat != nil {
		metrics.MemoryUtilizationRaw = float64(memStat.Used)
		metrics.MemoryUtilizationPercent = float32(memStat.UsedPercent)
	}

	if !s.initialized {
		s.lastTotal = total
		s.lastIdle = idle
		s.initialized = true
		// The first sample cannot compute a delta yet, so CPU metrics remain zero.
		return metrics, nil
	}

	deltaTotal := total - s.lastTotal
	deltaIdle := idle - s.lastIdle
	s.lastTotal = total
	s.lastIdle = idle

	metrics.CPUUtilizationPercent, metrics.CPUUtilizationRaw = cpuMetricsFromDelta(deltaTotal, deltaIdle)
	return metrics, nil
}

func (s *MetricsSampler) sampleContainerized() (mtrcs.ResourceMetrics, error) {
	usage, err := readCgroupV2CPUUsage()
	if err != nil {
		return mtrcs.ResourceMetrics{}, err
	}
	memUsage, memLimit, err := readCgroupV2Memory()
	if err != nil {
		return mtrcs.ResourceMetrics{}, err
	}
	quotaCores, err := readCgroupV2CPUQuotaCores()
	if err != nil || quotaCores <= 0 {
		quotaCores, err = readCgroupV2CPUSetCores()
		if err != nil {
			return mtrcs.ResourceMetrics{}, err
		}
	}

	now := time.Now()
	metrics := mtrcs.ResourceMetrics{}
	metrics.MemoryUtilizationRaw = float64(memUsage)
	if memLimit > 0 && !isUnlimitedMem(memLimit) {
		metrics.MemoryUtilizationPercent = float32(float64(memUsage) / float64(memLimit) * 100)
	}

	if s.lastSample.IsZero() {
		s.lastCPUUsage = usage
		s.lastSample = now
		return metrics, nil
	}

	elapsed := now.Sub(s.lastSample).Seconds()
	deltaUsage := usage - s.lastCPUUsage
	s.lastCPUUsage = usage
	s.lastSample = now

	if quotaCores <= 0 {
		return metrics, nil
	}

	if elapsed > 0 && deltaUsage >= 0 {
		usagePerCore := deltaUsage / elapsed
		metrics.CPUUtilizationPercent = float32((usagePerCore / quotaCores) * 100)
		// Raw CPU time uses the cgroup usage delta, which is already in seconds.
		metrics.CPUUtilizationRaw = deltaUsage * float64(time.Second)
	}

	return metrics, nil
}

func totalCPUTimes(stat *cpu.TimesStat) float64 {
	if stat == nil {
		return 0
	}
	return stat.User + stat.Nice + stat.System + stat.Idle + stat.Iowait + stat.Irq + stat.Softirq + stat.Steal + stat.Guest + stat.GuestNice
}

func cpuMetricsFromDelta(deltaTotal, deltaIdle float64) (float32, float64) {
	if deltaTotal <= 0 {
		return 0, 0
	}
	busy := deltaTotal - deltaIdle
	if busy < 0 {
		busy = 0
	}
	percent := float32((busy / deltaTotal) * 100)
	raw := busy * float64(time.Second)
	return percent, raw
}

func readCgroupV2CPUUsage() (float64, error) {
	path := "/sys/fs/cgroup/cpu.stat"
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[0] == "usage_usec" {
			value, err := strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return 0, err
			}
			return value / 1e6, nil
		}
	}
	return 0, errors.New("usage_usec not found")
}

// this will return 0 if the memory limit is unlimited
func readCgroupV2Memory() (uint64, uint64, error) {
	usage, err := readUint64("/sys/fs/cgroup/memory.current")
	if err != nil {
		return 0, 0, err
	}
	limit, err := readUint64OrMax("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0, 0, err
	}
	return usage, limit, nil
}

func readCgroupV2CPUQuotaCores() (float64, error) {
	path := "/sys/fs/cgroup/cpu.max"
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, errors.New("cpu.max format invalid")
	}
	if fields[0] == "max" {
		return 0, errors.New("cpu quota not set")
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, err
	}
	if quota <= 0 || period <= 0 {
		return 0, errors.New("cpu quota invalid")
	}
	return quota / period, nil
}

func readUint64(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(data))
	return strconv.ParseUint(value, 10, 64)
}

func readUint64OrMax(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(data))
	if value == "max" {
		return ^uint64(0), nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func readCgroupV2CPUSetCores() (float64, error) {
	data, err := os.ReadFile("/sys/fs/cgroup/cpuset.cpus.effective")
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(data))
	cores, err := parseCPUSet(value)
	if err != nil {
		return 0, err
	}
	if cores <= 0 {
		return 0, errors.New("cpuset empty")
	}
	return float64(cores), nil
}

func parseCPUSet(value string) (int, error) {
	if value == "" {
		return 0, errors.New("cpuset empty")
	}
	count := 0
	ranges := strings.SplitSeq(value, ",")
	for entry := range ranges {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, "-")
		if len(parts) == 1 {
			count++
			continue
		}
		if len(parts) != 2 {
			return 0, errors.New("cpuset format invalid")
		}
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}
		end, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, err
		}
		if end < start {
			return 0, errors.New("cpuset range invalid")
		}
		count += end - start + 1
	}
	return count, nil
}

func isUnlimitedMem(limit uint64) bool {
	return limit >= (1<<63 - 1)
}
