//go:build unit

package stats

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
)

func TestTotalCPUTimes(t *testing.T) {
	stat := cpu.TimesStat{
		User:      1,
		Nice:      2,
		System:    3,
		Idle:      4,
		Iowait:    5,
		Irq:       6,
		Softirq:   7,
		Steal:     8,
		Guest:     9,
		GuestNice: 10,
	}
	if got := totalCPUTimes(&stat); got != 55 {
		t.Fatalf("unexpected total cpu times: %v", got)
	}
}

func TestCPUMetricsFromDelta(t *testing.T) {
	percent, raw := cpuMetricsFromDelta(10, 4)
	if percent < 59.9 || percent > 60.1 {
		t.Fatalf("unexpected cpu percent: %v", percent)
	}
	if raw <= 0 {
		t.Fatalf("expected raw cpu to be positive")
	}
}

func TestParseCPUSet(t *testing.T) {
	cores, err := parseCPUSet("0-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cores != 4 {
		t.Fatalf("unexpected core count: %v", cores)
	}

	cores, err = parseCPUSet("0-1,4,6-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cores != 5 {
		t.Fatalf("unexpected core count: %v", cores)
	}
}
