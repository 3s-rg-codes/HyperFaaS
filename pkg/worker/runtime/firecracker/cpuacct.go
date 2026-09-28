package firecracker

import (
	"os"
	"strconv"
	"syscall"
)

// cpuAccountingEnabled gates the per-operation CPU log lines. Enable with
// HYPERFAAS_FC_CPU_ACCT=1 in the worker environment; "0"/"false" disables it.
func cpuAccountingEnabled() bool {
	raw := os.Getenv("HYPERFAAS_FC_CPU_ACCT")
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}

// readCPUSeconds returns cumulative CPU seconds charged to this process
// (RUSAGE_SELF) and to children that have already been reaped
// (RUSAGE_CHILDREN). Taking deltas around an operation attributes CPU to it:
// RUSAGE_SELF covers the Go worker (including its own syscall time) and
// RUSAGE_CHILDREN covers short-lived helpers such as the iptables binary and
// firecracker processes that exited during the window.
func readCPUSeconds() (self, children float64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
		self = timevalSeconds(ru.Utime) + timevalSeconds(ru.Stime)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru); err == nil {
		children = timevalSeconds(ru.Utime) + timevalSeconds(ru.Stime)
	}
	return self, children
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}
