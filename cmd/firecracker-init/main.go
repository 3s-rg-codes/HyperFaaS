package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const functionPath = "/function"

func main() {
	configureLogging()
	if err := mountRuntimeFilesystems(); err != nil {
		log.Printf("firecracker-init: mount setup failed: %v", err)
	}
	if err := wireConsole(); err != nil {
		log.Printf("firecracker-init: console setup failed: %v", err)
	}
	metadata, err := parseKernelMetadata("/proc/cmdline")
	if err != nil {
		log.Fatalf("firecracker-init: read kernel metadata: %v", err)
	}
	for key, value := range metadata {
		if err := os.Setenv(key, value); err != nil {
			log.Fatalf("firecracker-init: set %s: %v", key, err)
		}
	}
	for _, key := range []string{"CONTROLLER_ADDRESS", "INSTANCE_ID", "FUNCTION_ID"} {
		if os.Getenv(key) == "" {
			log.Fatalf("firecracker-init: %s is required", key)
		}
	}
	log.Printf("firecracker-init: starting function instance=%s function=%s controller=%s", os.Getenv("INSTANCE_ID"), os.Getenv("FUNCTION_ID"), os.Getenv("CONTROLLER_ADDRESS"))
	if err := syscall.Exec(functionPath, []string{functionPath}, os.Environ()); err != nil {
		log.Fatalf("firecracker-init: exec %s: %v", functionPath, err)
	}
}

func configureLogging() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if console, err := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		log.SetOutput(console)
	}
}

func wireConsole() error {
	console, err := os.OpenFile("/dev/console", os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	for _, fd := range []int{0, 1, 2} {
		if err := unix.Dup2(int(console.Fd()), fd); err != nil {
			return fmt.Errorf("dup console to fd %d: %w", fd, err)
		}
	}
	log.SetOutput(console)
	return nil
}

func mountRuntimeFilesystems() error {
	for _, dir := range []string{"/proc", "/sys", "/dev", "/tmp"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := unix.Mount("proc", "/proc", "proc", uintptr(unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV), ""); err != nil && err != unix.EBUSY {
		return fmt.Errorf("mount proc: %w", err)
	}
	if err := unix.Mount("sysfs", "/sys", "sysfs", uintptr(unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV), ""); err != nil && err != unix.EBUSY {
		return fmt.Errorf("mount sysfs: %w", err)
	}
	if err := unix.Mount("devtmpfs", "/dev", "devtmpfs", uintptr(unix.MS_NOSUID), "mode=0755"); err != nil && err != unix.EBUSY {
		return fmt.Errorf("mount devtmpfs: %w", err)
	}
	return nil
}

func parseKernelMetadata(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, field := range strings.Fields(string(raw)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "hyperfaas.controller":
			out["CONTROLLER_ADDRESS"] = value
		case "hyperfaas.instance_id":
			out["INSTANCE_ID"] = value
		case "hyperfaas.function_id":
			out["FUNCTION_ID"] = value
		case "hyperfaas.env_b64":
			decoded, err := base64.RawURLEncoding.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("decode hyperfaas.env_b64: %w", err)
			}
			for _, line := range strings.Split(string(decoded), "\n") {
				if line == "" {
					continue
				}
				envKey, envValue, ok := strings.Cut(line, "=")
				if ok && envKey != "" {
					out[envKey] = envValue
				}
			}
		}
	}
	return out, nil
}
