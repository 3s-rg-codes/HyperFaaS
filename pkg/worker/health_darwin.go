package worker

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// macOS has no /proc. These sysctl-backed readers exist so a worker can run
// locally for development; they are coarser than the Linux ones.

func readLoadavg1() (float64, error) {
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(b) < 24 {
		return 0, fmt.Errorf("unexpected vm.loadavg size %d", len(b))
	}
	fscale := binary.LittleEndian.Uint64(b[16:24])
	if fscale == 0 {
		return 0, errors.New("vm.loadavg has zero fscale")
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / float64(fscale), nil
}

// readCPUStat is unsupported without Mach host_statistics (cgo); cpuAllocated
// reports zero when it fails.
func readCPUStat() (uint64, uint64, error) {
	return 0, 0, errors.New("host cpu counters are not available on darwin")
}

func readMeminfo() (uint64, uint64, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	// Percentage of memory the kernel considers available (free + reclaimable),
	// the closest cheap analogue to MemAvailable.
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil {
		return 0, 0, err
	}
	if level > 100 {
		level = 100
	}
	return total, total / 100 * uint64(level), nil
}
