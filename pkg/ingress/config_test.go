package ingress_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hyperfaas-ideal-arch/pkg/ingress"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ingress.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigMissingGRPCProxyAddress(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    invocation_address: "127.0.0.1:50050"
    grpc_proxy_address: "127.0.0.1:50053"
routing:
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "server.grpc_proxy_address is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigDuplicateLeafID(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
  grpc_proxy_address: "127.0.0.1:50055"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    invocation_address: "127.0.0.1:50050"
    grpc_proxy_address: "127.0.0.1:50053"
  - id: 1
    invocation_address: "127.0.0.1:50060"
    grpc_proxy_address: "127.0.0.1:50063"
routing:
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must be unique") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigConflictingListenAddresses(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
  grpc_proxy_address: "127.0.0.1:8080"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    invocation_address: "127.0.0.1:50050"
    grpc_proxy_address: "127.0.0.1:50053"
routing:
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigInvalidLeafTransport(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
  grpc_proxy_address: "127.0.0.1:50055"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    invocation_address: "127.0.0.1:50050"
    grpc_proxy_address: "127.0.0.1:50053"
routing:
  leaf_transport: carrier-pigeon
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "routing.leaf_transport must be") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigMissingLeafInvocationAddress(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
  grpc_proxy_address: "127.0.0.1:50055"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    grpc_proxy_address: "127.0.0.1:50053"
routing:
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invocation_address is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigMissingLeafGRPCProxyAddress(t *testing.T) {
	path := writeTempConfig(t, `
node_id: ingress-1
logging:
  level: info
  format: text
server:
  http_address: "127.0.0.1:8080"
  grpc_proxy_address: "127.0.0.1:50055"
controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
leaves:
  - id: 1
    invocation_address: "127.0.0.1:50050"
routing:
  state_sync_interval: 5s
`)
	_, err := ingress.LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "grpc_proxy_address is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
