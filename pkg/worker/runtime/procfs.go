package runtime

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// RSSBytes reads the RSS memory usage of a process in bytes from /proc/<pid>/statm.
func RSSBytes(pid int) (uint64, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, scanner.Err()
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected statm format")
	}
	rssPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return rssPages * uint64(os.Getpagesize()), nil
}

// CPUNanoseconds reads the cumulative CPU user+system time of a process in nanoseconds.
// It assumes standard HZ=100.
func CPUNanoseconds(pid int) (uint64, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var data [2048]byte
	n, err := file.Read(data[:])
	if err != nil && err != io.EOF {
		return 0, err
	}

	fields := strings.Fields(string(data[:n]))
	if len(fields) < 15 {
		return 0, fmt.Errorf("unexpected stat format")
	}

	utime, err := strconv.ParseUint(fields[13], 10, 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseUint(fields[14], 10, 64)
	if err != nil {
		return 0, err
	}

	// Standard HZ is 100 clock ticks per second on Linux.
	// 1 tick = 10ms = 10,000,000 ns.
	const nsPerTick = 10000000
	return (utime + stime) * nsPerTick, nil
}
