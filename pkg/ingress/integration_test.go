package ingress_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
	"hyperfaas-ideal-arch/pkg/ingress"
	"hyperfaas-ideal-arch/pkg/ingress/grpcproxy"
	"hyperfaas-ideal-arch/pkg/ingress/routing"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
)

const (
	pollInterval   = 50 * time.Millisecond
	defaultTimeout = 15 * time.Second
)

func pollUntil(t *testing.T, timeout time.Duration, desc string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

type fakeLeaf struct {
	id         uint64
	invokeAddr string
	proxyAddr  string
	httpAddr   string
	state      *core.LeafState
	mu         sync.Mutex
	httpFn     func(http.ResponseWriter, *http.Request)
	proxySeen  chan metadata.MD
}

func startFakeLeaf(t *testing.T, leaf *fakeLeaf) {
	t.Helper()

	invokeLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	leaf.invokeAddr = invokeLis.Addr().String()

	grpcServer := grpc.NewServer()
	leafpkg.RegisterLeafControlServiceServer(grpcServer, &fakeLeafControl{leaf: leaf})
	go func() { _ = grpcServer.Serve(invokeLis) }()
	t.Cleanup(func() { grpcServer.Stop() })

	httpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaf.mu.Lock()
		fn := leaf.httpFn
		leaf.mu.Unlock()
		if fn != nil {
			fn(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Leaf-ID", strconv.FormatUint(leaf.id, 10))
		if custom := r.Header.Get("X-Custom"); custom != "" {
			w.Header().Set("X-Received-Custom", custom)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: h2c.NewHandler(httpHandler, &http2.Server{})}
	go func() { _ = httpServer.Serve(httpLis) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	leaf.httpAddr = httpLis.Addr().String()

	proxyLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	leaf.proxyAddr = proxyLis.Addr().String()
	leaf.proxySeen = make(chan metadata.MD, 4)

	proxyServer := grpc.NewServer(
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(func(ctx context.Context, _ string) (proxy.Mode, []proxy.Backend, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			select {
			case leaf.proxySeen <- md.Copy():
			default:
			}
			return proxy.One2One, []proxy.Backend{&echoBackend{t: t}}, nil
		})),
	)
	go func() { _ = proxyServer.Serve(proxyLis) }()
	t.Cleanup(func() { proxyServer.Stop() })
}

type fakeLeafControl struct {
	leafpkg.UnimplementedLeafControlServiceServer
	leaf *fakeLeaf
}

func (f *fakeLeafControl) CurrentState(context.Context, *leafpkg.CurrentLeafStateRequest) (*core.LeafState, error) {
	f.leaf.mu.Lock()
	defer f.leaf.mu.Unlock()
	return f.leaf.state, nil
}

// WatchRoutingState serves one full frame for the requested projection, then
// holds the stream open, mirroring a connected leaf.
func (f *fakeLeafControl) WatchRoutingState(req *leafpkg.WatchRoutingStateRequest, stream grpc.ServerStreamingServer[leafpkg.RoutingStateFrame]) error {
	f.leaf.mu.Lock()
	state := f.leaf.state
	f.leaf.mu.Unlock()

	frame := &leafpkg.RoutingStateFrame{
		ConfigVersion:  req.GetConfigVersion(),
		LeafId:         state.GetLeafId(),
		Revision:       1,
		FullSnapshot:   true,
		HealthyWorkers: 1,
	}
	if !state.GetHealthy() {
		frame.HealthyWorkers = 0
	}
	if req.GetProjection().GetFunctionCapacity() {
		frame.Capacities = state.GetFunctions()
	}
	if req.GetProjection().GetLeafLoad() {
		load := state.GetLeafLoad()
		frame.LeafLoad = &load
	}
	if req.GetProjection().GetAggregateInFlight() {
		var total uint64
		for _, c := range state.GetFunctions() {
			total += c.GetInFlight()
		}
		frame.AggregateInFlight = &total
	}
	if err := stream.Send(frame); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

type echoBackend struct {
	t *testing.T
}

func (b *echoBackend) String() string { return "echo" }

func (b *echoBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	return ctx, nil, status.Error(codes.Unimplemented, "unused")
}

func (b *echoBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) { return resp, nil }

func (b *echoBackend) BuildError(bool, error) ([]byte, error) { return nil, nil }

func ingressTestConfig(leaves ...ingress.LeafEndpoint) ingress.IngressConfig {
	return ingress.IngressConfig{
		NodeID:  "test-ingress",
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
		Server: ingress.ServerConfig{
			HTTPAddress:      "127.0.0.1:0",
			GRPCProxyAddress: "127.0.0.1:0",
		},
		ControlPlane: config.ControlPlaneConfig{Address: "127.0.0.1:1", DialTimeout: 2 * time.Second},
		Leaves:       leaves,
		Routing: ingress.RoutingConfig{
			StateSyncInterval: 100 * time.Millisecond,
		},
	}
}

func startIngress(t *testing.T, cfg ingress.IngressConfig) (httpAddr, proxyAddr string, rt *ingress.Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	t.Cleanup(cancel)

	httpLis, err := net.Listen("tcp", cfg.Server.HTTPAddress)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.HTTPAddress = httpLis.Addr().String()
	httpLis.Close()

	proxyLis, err := net.Listen("tcp", cfg.Server.GRPCProxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.GRPCProxyAddress = proxyLis.Addr().String()
	proxyLis.Close()

	logger := utils.SetupLogger(cfg.Logging)
	rt, err = ingress.Bootstrap(ctx, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Bootstrap no longer applies a static YAML policy. In production the
	// control plane delivers the first routing policy through the config watch;
	// tests stand in for that watch by applying a document directly.
	rt.ApplyPlatformConfig(&core.PlatformConfig{
		Version: 1,
		Routing: &core.RoutingPolicyConfig{
			Policy: &core.RoutingPolicyConfig_AvailableCapacity{
				AvailableCapacity: &core.AvailableCapacityRoutingPolicy{},
			},
		},
	})
	srv, err := ingress.NewServer(cfg, logger,
		ingress.WithRouting(rt.Engine),
		ingress.WithGRPCProxy(ingress.NewGRPCProxyFromRuntime(cfg, rt, logger)),
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Run(ctx) }()

	pollUntil(t, 5*time.Second, "ingress listeners", func() bool {
		for _, addr := range []string{cfg.Server.HTTPAddress, cfg.Server.GRPCProxyAddress} {
			conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
		}
		return true
	})

	return cfg.Server.HTTPAddress, cfg.Server.GRPCProxyAddress, rt
}

// TestBootstrapWithUnreachableLeafLeavesRoutingUnavailable documents the design
// choice that a leaf which cannot be dialed is skipped: the ingress still
// starts, but routing has no healthy leaf until one becomes reachable. There is
// no preparation timeout and no fallback to the previous policy.
func TestBootstrapWithUnreachableLeafLeavesRoutingUnavailable(t *testing.T) {
	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                1,
		InvocationAddress: "127.0.0.1:1",
		GRPCProxyAddress:  "127.0.0.1:2",
	})
	rt, err := ingress.Bootstrap(context.Background(), cfg, utils.SetupLogger(cfg.Logging))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if _, err := rt.Engine.Pick(routing.RouteRequest{FunctionID: 1}); err == nil {
		t.Fatal("expected routing to be unavailable with no reachable leaf")
	}
}

func TestBootstrapAppliesLeafStateBeforeServing(t *testing.T) {
	leaf := &fakeLeaf{
		id: 1,
		state: &core.LeafState{
			LeafId:  1,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 99, AvailableConcurrency: 10},
			},
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    1,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	_, _, rt := startIngress(t, cfg)

	target, err := rt.Engine.Pick(routing.RouteRequest{FunctionID: 99})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if target.LeafID != 1 {
		t.Fatalf("leaf = %d, want 1", target.LeafID)
	}
}

func TestHTTPInvokeForwardsToSelectedLeaf(t *testing.T) {
	leaf := &fakeLeaf{
		id: 1,
		state: &core.LeafState{
			LeafId:  1,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 7, AvailableConcurrency: 10},
			},
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    1,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	body := []byte("hello-http")
	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "7")
	req.Header.Set("X-Custom", "keep-me")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %q", resp.StatusCode, out)
	}
	if string(out) != string(body) {
		t.Fatalf("body = %q", out)
	}
	if resp.Header.Get("X-Leaf-ID") != "1" {
		t.Fatalf("leaf header = %q", resp.Header.Get("X-Leaf-ID"))
	}
}

func TestIngressUsesH2CToLeaf(t *testing.T) {
	seenProtocol := make(chan int, 1)
	leaf := &fakeLeaf{
		id: 12,
		state: &core.LeafState{
			LeafId:  12,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 120, AvailableConcurrency: 10},
			},
		},
		httpFn: func(w http.ResponseWriter, request *http.Request) {
			seenProtocol <- request.ProtoMajor
			w.WriteHeader(http.StatusOK)
		},
	}
	startFakeLeaf(t, leaf)
	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    leaf.id,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	request, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-HyperFaaS-User-ID", "1")
	request.Header.Set("X-HyperFaaS-Function-ID", "120")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if protocol := <-seenProtocol; protocol != 2 {
		t.Fatalf("ingress-to-leaf HTTP major version = %d, want 2", protocol)
	}
}

func TestColdCacheFallbackSelectsConfiguredLeaf(t *testing.T) {
	leaf := &fakeLeaf{
		id: 3,
		state: &core.LeafState{
			LeafId:    3,
			Healthy:   true,
			Functions: nil,
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    3,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader([]byte("cold")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "404")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %q", resp.StatusCode, body)
	}
}

func TestLeafFailureIsNotReplayed(t *testing.T) {
	leafA := &fakeLeaf{
		id: 10,
		state: &core.LeafState{
			LeafId:  10,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 50, AvailableConcurrency: 100},
			},
		},
	}
	leafB := &fakeLeaf{
		id: 11,
		state: &core.LeafState{
			LeafId:  11,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 50, AvailableConcurrency: 5},
			},
		},
	}

	var calls sync.Map
	leafA.httpFn = func(w http.ResponseWriter, _ *http.Request) {
		calls.Store("a", true)
		http.Error(w, "leaf unavailable", http.StatusServiceUnavailable)
	}
	leafB.httpFn = func(w http.ResponseWriter, _ *http.Request) {
		calls.Store("b", true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}

	startFakeLeaf(t, leafA)
	startFakeLeaf(t, leafB)

	cfg := ingressTestConfig(
		ingress.LeafEndpoint{ID: 10, InvocationAddress: leafA.invokeAddr, HTTPInvocationAddress: leafA.httpAddr, GRPCProxyAddress: leafA.proxyAddr},
		ingress.LeafEndpoint{ID: 11, InvocationAddress: leafB.invokeAddr, HTTPInvocationAddress: leafB.httpAddr, GRPCProxyAddress: leafB.proxyAddr},
	)
	httpAddr, _, _ := startIngress(t, cfg)

	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader([]byte("once")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "50")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %q", resp.StatusCode, body)
	}
	if _, ok := calls.Load("a"); !ok {
		t.Fatal("expected first attempt on leaf A")
	}
	if _, ok := calls.Load("b"); ok {
		t.Fatal("a failed invocation must not be replayed to a second leaf")
	}
}

func TestNoHealthyLeavesReturnsUnavailable(t *testing.T) {
	leaf := &fakeLeaf{
		id:    20,
		state: &core.LeafState{LeafId: 20, Healthy: false},
	}
	startFakeLeaf(t, leaf)
	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    20,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "999")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %q", resp.StatusCode, body)
	}
}

func TestTransparentGRPCProxyRoutesToLeafProxy(t *testing.T) {
	leaf := &fakeLeaf{
		id: 30,
		state: &core.LeafState{
			LeafId:  30,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 300, AvailableConcurrency: 2},
			},
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    30,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	_, proxyAddr, _ := startIngress(t, cfg)

	conn, err := grpc.NewClient(proxyAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority("300"),
		grpc.WithDefaultCallOptions(grpc.ForceCodecV2(proxy.Codec())),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := echopb.NewEchoClient(conn)
	_, _ = client.Echo(context.Background(), &echopb.EchoRequest{Data: []byte("hi")})

	select {
	case md := <-leaf.proxySeen:
		if got := md.Get(grpcproxy.MetadataFunctionID); len(got) == 0 || got[0] != "300" {
			t.Fatalf("x-hyperfaas-function-id = %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leaf proxy did not receive proxied request")
	}
}

func TestTransparentGRPCProxyInvalidFunctionID(t *testing.T) {
	leaf := &fakeLeaf{
		id:    31,
		state: &core.LeafState{LeafId: 31, Healthy: true},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    31,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	_, proxyAddr, _ := startIngress(t, cfg)

	conn, err := grpc.NewClient(proxyAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority("not-valid"),
		grpc.WithDefaultCallOptions(grpc.ForceCodecV2(proxy.Codec())),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := echopb.NewEchoClient(conn)
	_, err = client.Echo(context.Background(), &echopb.EchoRequest{Data: []byte("hi")})
	if err == nil {
		t.Fatal("expected error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v err = %v", status.Code(err), err)
	}
}

func TestAvailableCapacityRoutingPicksHighestCapacityLeaf(t *testing.T) {
	leafLow := &fakeLeaf{
		id: 40,
		state: &core.LeafState{
			LeafId:  40,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 400, AvailableConcurrency: 1},
			},
		},
	}
	leafHigh := &fakeLeaf{
		id: 41,
		state: &core.LeafState{
			LeafId:  41,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 400, AvailableConcurrency: 100},
			},
		},
	}
	startFakeLeaf(t, leafLow)
	startFakeLeaf(t, leafHigh)

	cfg := ingressTestConfig(
		ingress.LeafEndpoint{ID: 40, InvocationAddress: leafLow.invokeAddr, HTTPInvocationAddress: leafLow.httpAddr, GRPCProxyAddress: leafLow.proxyAddr},
		ingress.LeafEndpoint{ID: 41, InvocationAddress: leafHigh.invokeAddr, HTTPInvocationAddress: leafHigh.httpAddr, GRPCProxyAddress: leafHigh.proxyAddr},
	)
	httpAddr, _, _ := startIngress(t, cfg)

	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader([]byte("route")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "400")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Leaf-ID") != "41" {
		t.Fatalf("selected leaf = %q", resp.Header.Get("X-Leaf-ID"))
	}
}

// TestStreamingRequestNotBufferedAtIngress proves the ingress proxy does not
// read the whole body before forwarding: the leaf sees the first chunk while
// the client still holds the pipe open.
func TestStreamingRequestNotBufferedAtIngress(t *testing.T) {
	leaf := &fakeLeaf{
		id: 60,
		state: &core.LeafState{
			LeafId:  60,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 600, AvailableConcurrency: 10},
			},
		},
	}
	firstChunk := make(chan struct{})
	leaf.httpFn = func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		close(firstChunk)
		rest, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
		_, _ = w.Write(rest)
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    60,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-HyperFaaS-User-ID", "1")
	req.Header.Set("X-HyperFaaS-Function-ID", "600")

	writeErr := make(chan error, 1)
	go func() {
		if _, err := pw.Write([]byte("hello")); err != nil {
			writeErr <- err
			return
		}
		select {
		case <-firstChunk:
		case <-time.After(3 * time.Second):
			err := fmt.Errorf("leaf did not receive the first chunk before the body completed")
			_ = pw.CloseWithError(err)
			writeErr <- err
			return
		}
		_, err := pw.Write([]byte(" world"))
		if closeErr := pw.Close(); err == nil {
			err = closeErr
		}
		writeErr <- err
	}()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello world" {
		t.Fatalf("body = %q", body)
	}
}

// TestDirectLeafAndIngressResponsesMatch checks response equivalence between a
// direct leaf HTTP call and the same call through ingress.
func TestDirectLeafAndIngressResponsesMatch(t *testing.T) {
	leaf := &fakeLeaf{
		id: 70,
		state: &core.LeafState{
			LeafId:  70,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 700, AvailableConcurrency: 10},
			},
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    70,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	payload := []byte("equivalence")
	call := func(addr string) (int, []byte, string) {
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/invoke", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-HyperFaaS-User-ID", "1")
		req.Header.Set("X-HyperFaaS-Function-ID", "700")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body, resp.Header.Get("X-Leaf-ID")
	}

	directStatus, directBody, directLeaf := call(leaf.httpAddr)
	viaStatus, viaBody, viaLeaf := call(httpAddr)
	if directStatus != viaStatus || directLeaf != viaLeaf || !bytes.Equal(directBody, viaBody) {
		t.Fatalf("direct=(%d,%q,%s) via=(%d,%q,%s)", directStatus, directBody, directLeaf, viaStatus, viaBody, viaLeaf)
	}
}

// TestStreamingConcurrentLoadIngress forwards many request bodies through the
// ingress streaming proxy and requires complete responses. It guards against
// golang/go#40747-style truncation when the response exceeds the write buffer.
func TestStreamingConcurrentLoadIngress(t *testing.T) {
	leaf := &fakeLeaf{
		id: 90,
		state: &core.LeafState{
			LeafId:  90,
			Healthy: true,
			Functions: []*core.FunctionCapacity{
				{FunctionId: 900, AvailableConcurrency: 0},
			},
		},
	}
	startFakeLeaf(t, leaf)

	cfg := ingressTestConfig(ingress.LeafEndpoint{
		ID:                    90,
		InvocationAddress:     leaf.invokeAddr,
		HTTPInvocationAddress: leaf.httpAddr,
		GRPCProxyAddress:      leaf.proxyAddr,
	})
	httpAddr, _, _ := startIngress(t, cfg)

	const workers = 32
	const perWorker = 200
	payload := bytes.Repeat([]byte("x"), 4096)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 30 * time.Second}
			for i := 0; i < perWorker; i++ {
				req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/invoke", bytes.NewReader(payload))
				if err != nil {
					errCh <- err
					return
				}
				req.Header.Set("X-HyperFaaS-User-ID", "1")
				req.Header.Set("X-HyperFaaS-Function-ID", "900")
				resp, err := client.Do(req)
				if err != nil {
					errCh <- err
					return
				}
				out, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					errCh <- err
					return
				}
				if resp.StatusCode != http.StatusOK || !bytes.Equal(out, payload) {
					errCh <- fmt.Errorf("status=%d len=%d", resp.StatusCode, len(out))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent ingress invoke failed: %v", err)
	}
}
