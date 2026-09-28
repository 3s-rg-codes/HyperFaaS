package leaf_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"

	sideroproxy "github.com/siderolabs/grpc-proxy/proxy"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/controlplane/store/memory"
	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/core/config"
	"hyperfaas-ideal-arch/pkg/core/utils"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
	"hyperfaas-ideal-arch/pkg/leaf/grpcproxy"
	"hyperfaas-ideal-arch/pkg/leaf/runtime"
	leafworker "hyperfaas-ideal-arch/pkg/worker"
)

const (
	testLeafID     = 42
	testUserID     = 1
	pollInterval   = 50 * time.Millisecond
	defaultTimeout = 15 * time.Second
)

func testLeafConfig(cpAddr, workerAddr, leafAddr string) leafpkg.LeafConfig {
	return leafpkg.LeafConfig{
		LeafID: testLeafID,
		NodeID: "test-leaf",
		Logging: config.LoggingConfig{
			Level:  "error",
			Format: "text",
		},
		Server:  config.ServerConfig{ListenAddress: leafAddr},
		Workers: []leafpkg.WorkerEndpoint{{Address: workerAddr}},
		Dataplane: leafpkg.DataplaneConfig{
			ScaleToZeroAfter:              2 * time.Second,
			MaxInstancesPerWorker:         4,
			DialTimeout:                   2 * time.Second,
			StartTimeout:                  5 * time.Second,
			StopTimeout:                   2 * time.Second,
			StatusBackoff:                 100 * time.Millisecond,
			RoutingStateHeartbeatInterval: 100 * time.Millisecond,
			HTTPMaxIdleConns:              1024,
			HTTPMaxIdleConnsPerHost:       1024,
			HTTPIdleConnTimeout:           30 * time.Second,
		},
		Autoscaling: leafpkg.AutoscalingConfig{
			ReconcileInterval:          200 * time.Millisecond,
			UnlimitedConcurrencyTarget: 100,
		},
		ControlPlane: config.ControlPlaneConfig{
			Address:     cpAddr,
			DialTimeout: 2 * time.Second,
		},
	}
}

func pollUntil(t testing.TB, timeout time.Duration, desc string, fn func() bool) {
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

type testEnv struct {
	t         testing.TB
	ctx       context.Context
	cancel    context.CancelFunc
	store     *memory.Store
	cpAddr    string
	leafAddr  string
	httpAddr  string
	proxyAddr string
	rt        *runtime.Runtime
	control   leafpkg.LeafControlServiceClient
	sandbox   *fakeSandbox
}

type testEnvOption func(*testEnvConfig)

type testEnvConfig struct {
	enableGRPCProxy bool
}

func withGRPCProxy() testEnvOption {
	return func(c *testEnvConfig) { c.enableGRPCProxy = true }
}

func startTestEnv(t testing.TB, opts ...testEnvOption) *testEnv {
	t.Helper()
	var cfgOpts testEnvConfig
	for _, opt := range opts {
		opt(&cfgOpts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)

	cpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cpAddr := cpLis.Addr().String()
	cpLis.Close()

	store := memory.New()
	cpSrv, err := controlplane.NewServer(controlplane.ControlPlaneConfig{
		Server: controlplane.ServerConfig{GRPCAddress: cpAddr, HTTPAddress: "127.0.0.1:0"},
	}, utils.SetupLogger(config.LoggingConfig{Level: "error", Format: "text"}), controlplane.WithStore(store))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = cpSrv.Run(ctx) }()

	sandbox := newFakeSandbox()
	workerAddr := startFakeWorker(t, ctx, sandbox)

	leafLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	leafAddr := leafLis.Addr().String()
	leafLis.Close()

	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpAddr := httpLis.Addr().String()
	httpLis.Close()

	cfg := testLeafConfig(cpAddr, workerAddr, leafAddr)
	cfg.HTTPInvocationAddress = httpAddr
	logger := utils.SetupLogger(cfg.Logging)

	var proxyAddr string
	if cfgOpts.enableGRPCProxy {
		proxyLis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		proxyAddr = proxyLis.Addr().String()
		proxyLis.Close()
		cfg.GRPCProxy.ListenAddress = proxyAddr
	}

	rt, err := runtime.NewRuntime(ctx, cfg, nil, logger)
	if err != nil {
		t.Fatal(err)
	}

	if cfgOpts.enableGRPCProxy {
		go func() { _ = grpcproxy.Run(ctx, rt, cfg, logger) }()
		pollUntil(t, 5*time.Second, "grpc proxy listener", func() bool {
			conn, err := net.DialTimeout("tcp", proxyAddr, 100*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		})
	}

	leafSrv, err := leafpkg.NewServer(cfg, logger,
		leafpkg.WithControlService(rt),
		leafpkg.WithStateReporter(rt),
		leafpkg.WithFunctionRegistry(rt),
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = leafSrv.Run(ctx) }()

	pollUntil(t, 5*time.Second, "leaf HTTP invocation listener", func() bool {
		conn, err := net.DialTimeout("tcp", httpAddr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})

	conn, err := grpc.NewClient(leafAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &testEnv{
		t:         t,
		ctx:       ctx,
		cancel:    cancel,
		store:     store,
		cpAddr:    cpAddr,
		leafAddr:  leafAddr,
		httpAddr:  httpAddr,
		proxyAddr: proxyAddr,
		rt:        rt,
		control:   leafpkg.NewLeafControlServiceClient(conn),
		sandbox:   sandbox,
	}
}

func (e *testEnv) close() {
	e.cancel()
	e.rt.Close()
}

func (e *testEnv) invokeHTTP(functionID uint64, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(e.ctx, http.MethodPost, "http://"+e.httpAddr+leafpkg.HTTPInvokePath, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("X-HyperFaaS-User-ID", strconv.FormatUint(testUserID, 10))
	request.Header.Set("X-HyperFaaS-Function-ID", strconv.FormatUint(functionID, 10))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	out, err := io.ReadAll(response.Body)
	return out, response.StatusCode, err
}

func startFakeWorker(t testing.TB, ctx context.Context, sandbox *fakeSandbox) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	cfg := leafworker.WorkerConfig{
		NodeID:  "fake-worker",
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
		Server:  config.ServerConfig{ListenAddress: addr},
	}
	srv, err := leafworker.NewServer(cfg, utils.SetupLogger(cfg.Logging), leafworker.WithSandboxService(sandbox))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Run(ctx) }()
	return addr
}

// invokeGate blocks function handlers until Release is called.
type invokeGate struct {
	ch   chan struct{}
	once sync.Once
}

func newInvokeGate() *invokeGate {
	return &invokeGate{ch: make(chan struct{})}
}

func (g *invokeGate) Wait(ctx context.Context) error {
	select {
	case <-g.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *invokeGate) Release() {
	g.once.Do(func() { close(g.ch) })
}

type fakeSandbox struct {
	mu           sync.Mutex
	instances    map[uint64]*fakeInstance
	gate         *invokeGate
	inFlight     atomic.Int32
	cachedImages []*core.CachedImage
}

type fakeInstance struct {
	state      *core.InstanceState
	httpServer *http.Server
	grpcServer *grpc.Server
}

func newFakeSandbox() *fakeSandbox {
	return &fakeSandbox{instances: make(map[uint64]*fakeInstance)}
}

func (f *fakeSandbox) PrepareImage(_ context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	image := ""
	if function != nil && function.GetRuntime() != nil {
		image = function.GetRuntime().GetImage()
	}
	f.mu.Lock()
	if image != "" {
		f.cachedImages = append(f.cachedImages, &core.CachedImage{Image: image})
	}
	f.mu.Unlock()
	return &core.PreparedArtifact{FunctionId: function.GetFunctionId(), Image: image}, nil
}

func (f *fakeSandbox) HasImage(context.Context, string) (bool, error) { return true, nil }

func (f *fakeSandbox) CachedImages() []*core.CachedImage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*core.CachedImage, len(f.cachedImages))
	copy(out, f.cachedImages)
	return out
}

func (f *fakeSandbox) SetGate(g *invokeGate) {
	f.gate = g
}

func (f *fakeSandbox) CreateSandbox(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	protocol := req.GetFunction().GetRuntime().GetProtocol()
	maxCC := req.GetFunction().GetScale().GetMaxConcurrency()
	var availCC uint64
	if maxCC == 0 {
		availCC = 1000000
	} else {
		availCC = maxCC
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := lis.Addr().String()

	inst := &fakeInstance{}
	if protocol == "grpc" {
		grpcSrv := grpc.NewServer()
		echopb.RegisterEchoServer(grpcSrv, &echoGRPCServer{sandbox: f, ctx: ctx})
		inst.grpcServer = grpcSrv
		go func() { _ = grpcSrv.Serve(lis) }()
	} else {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if err := f.serveHTTP(r.Context(), w, r); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
		inst.httpServer = &http.Server{Handler: mux}
		go func() { _ = inst.httpServer.Serve(lis) }()
	}

	state := &core.InstanceState{
		InstanceId:           req.GetInstanceId(),
		FunctionId:           req.GetFunction().GetFunctionId(),
		WorkerId:             req.GetWorkerId(),
		Address:              addr,
		Protocol:             protocol,
		Ready:                true,
		MaxConcurrency:       maxCC,
		AvailableConcurrency: availCC,
	}
	inst.state = state
	f.mu.Lock()
	f.instances[req.GetInstanceId()] = inst
	f.mu.Unlock()
	return state, nil
}

func (f *fakeSandbox) serveHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	if g := f.gate; g != nil {
		if err := g.Wait(ctx); err != nil {
			return err
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

type echoGRPCServer struct {
	echopb.UnimplementedEchoServer
	sandbox *fakeSandbox
	ctx     context.Context
}

func (s *echoGRPCServer) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	s.sandbox.inFlight.Add(1)
	defer s.sandbox.inFlight.Add(-1)
	if g := s.sandbox.gate; g != nil {
		if err := g.Wait(ctx); err != nil {
			return nil, err
		}
	}
	return &echopb.EchoResponse{Data: req.GetData()}, nil
}

func (f *fakeSandbox) StopSandbox(_ context.Context, instanceID uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if inst, ok := f.instances[instanceID]; ok {
		if inst.httpServer != nil {
			_ = inst.httpServer.Close()
		}
		if inst.grpcServer != nil {
			inst.grpcServer.Stop()
		}
		delete(f.instances, instanceID)
	}
	return nil
}

func (f *fakeSandbox) ListSandboxes(context.Context) ([]*core.InstanceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*core.InstanceState, 0, len(f.instances))
	for _, inst := range f.instances {
		out = append(out, inst.state)
	}
	return out, nil
}

func (f *fakeSandbox) SignalReady(context.Context, uint64) error { return nil }

func (e *testEnv) waitForFunction(functionID uint64) {
	e.t.Helper()
	pollUntil(e.t, defaultTimeout, "function registered on leaf", func() bool {
		state, err := e.control.CurrentState(e.ctx, &leafpkg.CurrentLeafStateRequest{})
		if err != nil {
			return false
		}
		for _, cap := range state.GetFunctions() {
			if cap.GetFunctionId() == functionID && !cap.GetDeleted() {
				return true
			}
		}
		return false
	})
}

func (e *testEnv) readyInstances(functionID uint64) uint32 {
	state, err := e.control.CurrentState(e.ctx, &leafpkg.CurrentLeafStateRequest{})
	if err != nil {
		return 0
	}
	for _, cap := range state.GetFunctions() {
		if cap.GetFunctionId() == functionID && !cap.GetDeleted() {
			return cap.GetReadyInstances()
		}
	}
	return 0
}

func (e *testEnv) createHTTPFunction(id uint64) *core.FunctionSpec {
	return e.createHTTPFunctionWithScale(id, 1, 4)
}

func (e *testEnv) createHTTPFunctionWithScale(id uint64, maxConcurrency, maxInstances uint64) *core.FunctionSpec {
	e.t.Helper()
	if err := e.store.CreateUser(e.ctx, &core.UserSpec{UserId: testUserID, Name: "test"}); err != nil {
		e.t.Fatal(err)
	}
	fn := &core.FunctionSpec{
		UserId:     testUserID,
		FunctionId: id,
		Runtime: &core.RuntimeSpec{
			Image:     "fake://echo",
			Protocol:  "http",
			Isolation: core.IsolationKind_ISOLATION_KIND_FAKE,
		},
		Scale: &core.ScalePolicySpec{
			MaxConcurrency:         maxConcurrency,
			MaxInstances:           maxInstances,
			MaxQueueDepth:          64,
			ScaleToZeroIdleTimeout: durationpb.New(400 * time.Millisecond),
			ColdStartTimeout:       durationpb.New(5 * time.Second),
			RequestTimeout:         durationpb.New(5 * time.Second),
		},
	}
	if err := e.store.CreateFunction(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
	return fn
}

func (e *testEnv) createGRPCFunction(id uint64) *core.FunctionSpec {
	e.t.Helper()
	if err := e.store.CreateUser(e.ctx, &core.UserSpec{UserId: testUserID, Name: "test"}); err != nil {
		e.t.Fatal(err)
	}
	fn := &core.FunctionSpec{
		UserId:     testUserID,
		FunctionId: id,
		Runtime: &core.RuntimeSpec{
			Image:     "fake://echo-grpc",
			Protocol:  "grpc",
			Isolation: core.IsolationKind_ISOLATION_KIND_FAKE,
		},
		Scale: &core.ScalePolicySpec{
			MaxConcurrency:         1,
			MaxInstances:           4,
			MaxQueueDepth:          64,
			ScaleToZeroIdleTimeout: durationpb.New(400 * time.Millisecond),
			ColdStartTimeout:       durationpb.New(5 * time.Second),
			RequestTimeout:         durationpb.New(5 * time.Second),
		},
	}
	if err := e.store.CreateFunction(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
	return fn
}

func TestLeafIdentityAndBootstrap(t *testing.T) {
	env := startTestEnv(t)
	defer env.close()

	fn := env.createHTTPFunction(7)

	pollUntil(t, defaultTimeout, "function on leaf", func() bool {
		state, err := env.control.CurrentState(env.ctx, &leafpkg.CurrentLeafStateRequest{})
		if err != nil {
			return false
		}
		if state.GetLeafId() != testLeafID {
			return false
		}
		for _, cap := range state.GetFunctions() {
			if cap.GetFunctionId() == fn.GetFunctionId() {
				return true
			}
		}
		return false
	})
}

func TestHTTPInvokeColdStart(t *testing.T) {
	env := startTestEnv(t)
	defer env.close()

	fn := env.createHTTPFunction(11)
	env.waitForFunction(fn.GetFunctionId())

	body, statusCode, err := env.invokeHTTP(fn.GetFunctionId(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if statusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("status=%d body=%q", statusCode, body)
	}
}

func TestDeleteFunctionTombstone(t *testing.T) {
	env := startTestEnv(t)
	defer env.close()

	fn := env.createHTTPFunction(12)
	env.waitForFunction(fn.GetFunctionId())

	_, err := env.control.DeleteFunction(env.ctx, &leafpkg.DeleteLeafFunctionRequest{FunctionId: fn.GetFunctionId()})
	if err != nil {
		t.Fatal(err)
	}

	pollUntil(t, defaultTimeout, "delete tombstone", func() bool {
		state, err := env.control.CurrentState(env.ctx, &leafpkg.CurrentLeafStateRequest{})
		if err != nil {
			return false
		}
		for _, cap := range state.GetFunctions() {
			if cap.GetFunctionId() == fn.GetFunctionId() && cap.GetDeleted() {
				return true
			}
		}
		return false
	})

	_, statusCode, err := env.invokeHTTP(fn.GetFunctionId(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("status after delete = %d, want 404", statusCode)
	}
}

func TestFunctionIDFromAuthority(t *testing.T) {
	md := metadata.Pairs(":authority", "99")
	id, err := grpcproxy.FunctionIDFromAuthority(context.Background(), md)
	if err != nil || id != 99 {
		t.Fatalf("got id=%d err=%v", id, err)
	}
}

func TestGRPCProxyRoutesByAuthority(t *testing.T) {
	env := startTestEnv(t, withGRPCProxy())
	defer env.close()

	fn := env.createGRPCFunction(21)
	env.waitForFunction(fn.GetFunctionId())

	authority := strconv.FormatUint(fn.GetFunctionId(), 10)
	conn, err := grpc.NewClient(env.proxyAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(authority),
		grpc.WithDefaultCallOptions(grpc.ForceCodecV2(sideroproxy.Codec())),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := echopb.NewEchoClient(conn)
	payload := []byte("proxy-ping")
	resp, err := client.Echo(env.ctx, &echopb.EchoRequest{Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.GetData()) != string(payload) {
		t.Fatalf("unexpected echo data %q", resp.GetData())
	}

	// Second unary call reuses released capacity after the first stream completes.
	resp2, err := client.Echo(env.ctx, &echopb.EchoRequest{Data: []byte("proxy-ping-2")})
	if err != nil {
		t.Fatalf("second proxy call failed (lease may not be released): %v", err)
	}
	if string(resp2.GetData()) != "proxy-ping-2" {
		t.Fatalf("unexpected second echo %q", resp2.GetData())
	}
}

func TestConcurrentInvokeCreatesExtraInstances(t *testing.T) {
	env := startTestEnv(t)
	defer env.close()

	gate := newInvokeGate()
	env.sandbox.SetGate(gate)

	fn := env.createHTTPFunctionWithScale(31, 1, 4)
	env.waitForFunction(fn.GetFunctionId())

	const concurrent = 3
	errCh := make(chan error, concurrent)
	for range concurrent {
		go func() {
			_, _, err := env.invokeHTTP(fn.GetFunctionId(), []byte("hold"))
			errCh <- err
		}()
	}

	pollUntil(t, defaultTimeout, "extra instances for concurrent load", func() bool {
		return env.readyInstances(fn.GetFunctionId()) >= 2
	})

	gate.Release()

	for range concurrent {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	if n := env.readyInstances(fn.GetFunctionId()); n < 2 {
		t.Fatalf("expected at least 2 ready instances after concurrent invokes, got %d", n)
	}
}

func TestHTTPScaleDownAfterIdle(t *testing.T) {
	env := startTestEnv(t)
	defer env.close()

	fn := env.createHTTPFunction(41)
	env.waitForFunction(fn.GetFunctionId())

	body, statusCode, err := env.invokeHTTP(fn.GetFunctionId(), []byte("warm"))
	if err != nil {
		t.Fatal(err)
	}
	if statusCode != http.StatusOK || string(body) != "warm" {
		t.Fatalf("status=%d body=%q", statusCode, body)
	}

	pollUntil(t, defaultTimeout, "scale to zero", func() bool {
		return env.readyInstances(fn.GetFunctionId()) == 0
	})
}
