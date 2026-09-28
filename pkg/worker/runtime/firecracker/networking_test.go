package firecracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPortProxyAdvertiseIPUsesConfiguredWorkerAddress(t *testing.T) {
	original := routeLocalIP
	routeLocalIP = func() (string, error) {
		t.Fatal("route lookup must not run when proxy_advertise_address is configured")
		return "", nil
	}
	t.Cleanup(func() { routeLocalIP = original })

	got, err := portProxyAdvertiseIP("", "192.168.10.20", defaultInternalCIDR, defaultExposedCIDR)
	if err != nil {
		t.Fatalf("portProxyAdvertiseIP: %v", err)
	}
	if got != "192.168.10.20" {
		t.Fatalf("portProxyAdvertiseIP = %q, want configured worker address", got)
	}
}

func TestPortProxyAdvertiseIPRejectsSandboxRoute(t *testing.T) {
	original := routeLocalIP
	routeLocalIP = func() (string, error) { return "10.241.7.9", nil }
	t.Cleanup(func() { routeLocalIP = original })

	_, err := portProxyAdvertiseIP("0.0.0.0", "", defaultInternalCIDR, defaultExposedCIDR)
	if err == nil || !strings.Contains(err.Error(), defaultInternalCIDR) {
		t.Fatalf("portProxyAdvertiseIP error = %v, want sandbox CIDR rejection", err)
	}
}

func TestStartPortProxyBindsWildcardAndAdvertisesConfiguredAddress(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	proxy, address, err := startPortProxy(context.Background(), "", "127.0.0.1", target.Addr().String(), slog.Default(), defaultInternalCIDR, defaultExposedCIDR)
	if err != nil {
		t.Fatalf("startPortProxy: %v", err)
	}
	defer proxy.close()
	if host, _, err := net.SplitHostPort(address); err != nil || host != "127.0.0.1" {
		t.Fatalf("advertised address = %q, want 127.0.0.1:<port>", address)
	}
	if host, _, err := net.SplitHostPort(proxy.listener.Addr().String()); err != nil || !net.ParseIP(host).IsUnspecified() {
		t.Fatalf("listener address = %q, want wildcard", proxy.listener.Addr())
	}
}

func TestProxyPortConnectionLogsBackendDialFailureWithFiveSecondTimeout(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	client, peer := net.Pipe()
	defer peer.Close()
	target := "192.0.2.10:8080"
	dialErr := errors.New("backend unavailable")

	proxyPortConnection(context.Background(), client, target, logger, func(network, address string, timeout time.Duration) (net.Conn, error) {
		if network != "tcp" {
			t.Errorf("dial network = %q, want tcp", network)
		}
		if address != target {
			t.Errorf("dial address = %q, want %q", address, target)
		}
		if timeout != 5*time.Second {
			t.Errorf("dial timeout = %v, want 5s", timeout)
		}
		return nil, dialErr
	})

	output := logs.String()
	if !strings.Contains(output, `"backend_target":"`+target+`"`) {
		t.Fatalf("log = %s, want backend target", output)
	}
	if !strings.Contains(output, dialErr.Error()) {
		t.Fatalf("log = %s, want raw dial error", output)
	}
}

func TestPortProxyAdvertiseIPRejectsHffcSandboxCIDRDeterministically(t *testing.T) {
	for i := 0; i < 700; i++ {
		candidate := fmt.Sprintf("10.241.%d.%d", i/256, i%256)
		if _, err := portProxyAdvertiseIP("", candidate, defaultInternalCIDR, defaultExposedCIDR); err == nil {
			t.Fatalf("accepted hffc sandbox address %s", candidate)
		}
	}
}

func TestNetworkManagerRefillPolicyDefaults(t *testing.T) {
	mgr, err := newNetworkManager(
		defaultInternalCIDR, defaultExposedCIDR,
		true, 64, t.TempDir(),
		net.ParseIP(defaultGuestIP), net.ParseIP(defaultGatewayIP), defaultGuestMAC,
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("newNetworkManager: %v", err)
	}
	if !mgr.refillOnBorrow {
		t.Fatal("refillOnBorrow = false, want refill to start when a network is borrowed")
	}
	if mgr.refillInterval != defaultRefillInterval {
		t.Fatalf("refillInterval = %v, want %v", mgr.refillInterval, defaultRefillInterval)
	}
	if got := mgr.refillThreshold(); got != 16 {
		t.Fatalf("refillThreshold() = %d, want 16 for pool size 64", got)
	}
}

func TestNetworkManagerRefillPolicyEnvOverrides(t *testing.T) {
	t.Setenv("HYPERFAAS_FC_REFILL_ON_BORROW", "false")
	t.Setenv("HYPERFAAS_FC_REFILL_INTERVAL", "25ms")
	mgr, err := newNetworkManager(
		defaultInternalCIDR, defaultExposedCIDR,
		true, 128, t.TempDir(),
		net.ParseIP(defaultGuestIP), net.ParseIP(defaultGatewayIP), defaultGuestMAC,
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("newNetworkManager: %v", err)
	}
	if mgr.refillOnBorrow {
		t.Fatal("refillOnBorrow = true, want env override to disable borrow-triggered refill")
	}
	if mgr.refillInterval != 25*time.Millisecond {
		t.Fatalf("refillInterval = %v, want 25ms", mgr.refillInterval)
	}
	if got := mgr.refillThreshold(); got != 32 {
		t.Fatalf("refillThreshold() = %d, want 32 for pool size 128", got)
	}
}

func TestNetworkManagerRefillIntervalRejectsNegative(t *testing.T) {
	t.Setenv("HYPERFAAS_FC_REFILL_INTERVAL", "-1s")
	mgr, err := newNetworkManager(
		defaultInternalCIDR, defaultExposedCIDR,
		true, 32, t.TempDir(),
		net.ParseIP(defaultGuestIP), net.ParseIP(defaultGatewayIP), defaultGuestMAC,
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("newNetworkManager: %v", err)
	}
	if mgr.refillInterval != defaultRefillInterval {
		t.Fatalf("refillInterval = %v, want default %v after invalid override", mgr.refillInterval, defaultRefillInterval)
	}
}
