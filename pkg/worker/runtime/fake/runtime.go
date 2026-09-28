// Package fake provides an in-process worker runtime for local multi-node testing.
//
// Supported images (same tags as the Go function Docker images):
//   - echo-http / echo-grpc: echo the request payload
//   - sleep-http: sleep 1s (matching functions/go/sleep-http), then echo
//   - fib-http: CPU-bound naive Fibonacci (matching functions/go/fib-http), then echo
package fake

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	echopb "hyperfaas-ideal-arch/functions/go/echo-grpc/pb"
	"hyperfaas-ideal-arch/pkg/core"
)

const (
	ImageEchoHTTP  = "echo-http"
	ImageEchoGRPC  = "echo-grpc"
	ImageSleepHTTP = "sleep-http"
	ImageFibHTTP   = "fib-http"
)

// Config configures the in-process fake runtime.
type Config struct {
	// SimulateSandboxStartLatency delays Start with a heavy-tailed distribution
	// (p50≈100ms, p90≈3s, p99≈20s) to surface leaf locking under slow CreateSandbox.
	SimulateSandboxStartLatency bool
}

// Runtime serves function instances in-process on ephemeral localhost ports.
type Runtime struct {
	cfg    Config
	logger *slog.Logger

	mu        sync.Mutex
	instances map[uint64]*instance
}

type instance struct {
	state      *core.InstanceState
	httpServer *http.Server
	grpcServer *grpc.Server
	listener   net.Listener
	cold       atomic.Bool
	kind       string
}

// New creates a fake runtime.
func New(cfg Config, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		cfg:       cfg,
		logger:    logger.With("runtime", "fake"),
		instances: make(map[uint64]*instance),
	}
}

func (r *Runtime) Prepare(_ context.Context, function *core.FunctionSpec) (*core.PreparedArtifact, error) {
	image := normalizeImage(function.GetRuntime().GetImage())
	if !supportedImage(image) {
		return nil, fmt.Errorf("fake runtime: unsupported image %q", function.GetRuntime().GetImage())
	}
	return &core.PreparedArtifact{
		FunctionId: function.GetFunctionId(),
		Image:      image,
	}, nil
}

func (r *Runtime) HasImage(_ context.Context, image string) (bool, error) {
	return supportedImage(normalizeImage(image)), nil
}

func (r *Runtime) Start(ctx context.Context, req *core.StartSandboxRequest) (*core.InstanceState, error) {
	function := req.GetFunction()
	if function == nil || function.GetRuntime() == nil {
		return nil, fmt.Errorf("fake runtime: function runtime is required")
	}
	image := normalizeImage(function.GetRuntime().GetImage())
	if !supportedImage(image) {
		return nil, fmt.Errorf("fake runtime: unsupported image %q", function.GetRuntime().GetImage())
	}

	if r.cfg.SimulateSandboxStartLatency {
		delay := sampleStartLatency()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	protocol := strings.ToLower(strings.TrimSpace(function.GetRuntime().GetProtocol()))
	if protocol == "" {
		protocol = protocolForImage(image)
	}
	if image == ImageEchoGRPC {
		protocol = "grpc"
	}
	if image == ImageEchoHTTP || image == ImageSleepHTTP || image == ImageFibHTTP {
		protocol = "http"
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("fake runtime: listen: %w", err)
	}

	inst := &instance{
		listener: lis,
		kind:     image,
	}
	inst.cold.Store(true)

	switch protocol {
	case "grpc":
		grpcSrv := grpc.NewServer()
		echopb.RegisterEchoServer(grpcSrv, &echoGRPCServer{inst: inst})
		inst.grpcServer = grpcSrv
		go func() { _ = grpcSrv.Serve(lis) }()
	default:
		mux := http.NewServeMux()
		mux.HandleFunc("/", inst.serveHTTP)
		inst.httpServer = &http.Server{Handler: mux}
		go func() { _ = inst.httpServer.Serve(lis) }()
	}

	state := &core.InstanceState{
		InstanceId: req.GetInstanceId(),
		FunctionId: function.GetFunctionId(),
		WorkerId:   req.GetWorkerId(),
		Address:    lis.Addr().String(),
		Protocol:   protocol,
		Ready:      true,
		StartedAt:  timestamppb.Now(),
	}
	inst.state = state

	r.mu.Lock()
	if prev, ok := r.instances[req.GetInstanceId()]; ok {
		delete(r.instances, req.GetInstanceId())
		r.mu.Unlock()
		_ = stopInstance(prev)
		_ = stopInstance(inst)
		return nil, fmt.Errorf("fake runtime: instance %d already exists", req.GetInstanceId())
	}
	r.instances[req.GetInstanceId()] = inst
	r.mu.Unlock()

	r.logger.Info("fake sandbox started",
		"instance_id", req.GetInstanceId(),
		"function_id", function.GetFunctionId(),
		"image", image,
		"protocol", protocol,
		"address", state.Address,
	)
	return state, nil
}

func (r *Runtime) Stop(_ context.Context, instanceID uint64) error {
	r.mu.Lock()
	inst, ok := r.instances[instanceID]
	if ok {
		delete(r.instances, instanceID)
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return stopInstance(inst)
}

func (r *Runtime) Stats(_ context.Context, instanceID uint64) (*core.ResourceUsage, error) {
	r.mu.Lock()
	_, ok := r.instances[instanceID]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("fake runtime: instance %d not found", instanceID)
	}
	return &core.ResourceUsage{}, nil
}

func (inst *instance) serveHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if inst.kind == ImageSleepHTTP {
		time.Sleep(time.Second)
	}
	cold := "0"
	if inst.cold.CompareAndSwap(true, false) {
		cold = "1"
	}
	w.Header().Set("X-Cold", cold)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

type echoGRPCServer struct {
	echopb.UnimplementedEchoServer
	inst *instance
}

func (s *echoGRPCServer) Echo(_ context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	cold := s.inst.cold.CompareAndSwap(true, false)
	return &echopb.EchoResponse{Data: req.GetData(), Cold: cold}, nil
}

func stopInstance(inst *instance) error {
	if inst.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = inst.httpServer.Shutdown(ctx)
	}
	if inst.grpcServer != nil {
		inst.grpcServer.GracefulStop()
	}
	if inst.listener != nil {
		_ = inst.listener.Close()
	}
	return nil
}

func supportedImage(image string) bool {
	switch image {
	case ImageEchoHTTP, ImageEchoGRPC, ImageSleepHTTP, ImageFibHTTP:
		return true
	default:
		return false
	}
}

func normalizeImage(image string) string {
	image = strings.TrimSpace(image)
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	if i := strings.Index(image, ":"); i >= 0 {
		image = image[:i]
	}
	return image
}

func protocolForImage(image string) string {
	if image == ImageEchoGRPC {
		return "grpc"
	}
	return "http"
}

func randomSleepDuration() time.Duration {
	const min = time.Second
	const max = 1500 * time.Millisecond
	span := int64(max - min)
	return min + time.Duration(rand.Int64N(span+1))
}

// sampleStartLatency draws from a piecewise-linear inverse CDF with knots:
// p0≈20ms, p50=100ms, p90=3s, p99=20s, p100≈40s.
func sampleStartLatency() time.Duration {
	u := rand.Float64()
	type knot struct {
		p float64
		d time.Duration
	}
	knots := []knot{
		{0, 20 * time.Millisecond},
		{0.50, 100 * time.Millisecond},
		{0.90, 3 * time.Second},
		{0.99, 20 * time.Second},
		{1.00, 40 * time.Second},
	}
	for i := 1; i < len(knots); i++ {
		lo, hi := knots[i-1], knots[i]
		if u > hi.p {
			continue
		}
		span := hi.p - lo.p
		if span <= 0 {
			return hi.d
		}
		t := (u - lo.p) / span
		return lo.d + time.Duration(t*float64(hi.d-lo.d))
	}
	return knots[len(knots)-1].d
}
