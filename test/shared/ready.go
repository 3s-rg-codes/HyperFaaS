package shared

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func WaitForCluster(t *testing.T, cfg Config) {
	t.Helper()

	targets := []struct {
		name string
		addr string
	}{
		{name: "controlplane HTTP", addr: cfg.ControlPlaneHTTP},
		{name: "controlplane gRPC", addr: cfg.ControlPlaneGRPC},
		{name: "ingress HTTP", addr: cfg.IngressHTTP},
		{name: "ingress gRPC proxy", addr: cfg.IngressGRPCProxy},
	}

	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		allReady := true
		for _, target := range targets {
			if err := tcpReady(target.addr); err != nil {
				lastErr = fmt.Errorf("%s (%s): %w", target.name, target.addr, err)
				allReady = false
				break
			}
		}
		if allReady {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("cluster not reachable: %v\nstart the cluster first with: just start", lastErr)
}

func tcpReady(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return err
	}
	return conn.Close()
}
