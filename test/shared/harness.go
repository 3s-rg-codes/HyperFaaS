package shared

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"hyperfaas-ideal-arch/pkg/core"
)

const (
	EchoHTTPImage  = "echo-http"
	EchoGRPCImage  = "echo-grpc"
	SleepHTTPImage = "sleep-http"
	FibHTTPImage   = "fib-http"
)

type Harness struct {
	T               *testing.T
	Cfg             Config
	Log             *slog.Logger
	CP              *ControlPlaneClient
	ScaleToZeroIdle time.Duration
}

var (
	logInitOnce sync.Once
	logFile     *os.File
	initLogErr  error
)

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func detectLogName() string {
	for i := 1; i < 15; i++ {
		_, file, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		if strings.Contains(file, "/test/dst") {
			return "test_dst.log"
		}
		if strings.Contains(file, "/test/perf") {
			return "test_perf.log"
		}
	}
	return "test_unknown.log"
}

func getLogger(t *testing.T) *slog.Logger {
	logInitOnce.Do(func() {
		repoRoot := findRepoRoot()
		logDir := filepath.Join(repoRoot, ".run", "logs")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			initLogErr = err
			return
		}

		logName := detectLogName()
		filePath := filepath.Join(logDir, logName)
		// Truncate the file on start
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			initLogErr = err
			return
		}
		logFile = f
	})

	var writers []io.Writer
	writers = append(writers, os.Stderr)
	if logFile != nil {
		writers = append(writers, logFile)
	}

	mw := io.MultiWriter(writers...)
	handler := slog.NewTextHandler(mw, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	return slog.New(handler).With("test", t.Name())
}

func NewHarness(t *testing.T) *Harness {
	t.Helper()
	cfg := LoadConfig()
	WaitForCluster(t, cfg)
	wcfg := LoadWorkloadConfig()
	log := getLogger(t)
	if initLogErr != nil {
		t.Fatalf("failed to initialize logger: %v", initLogErr)
	}
	h := &Harness{
		T:               t,
		Cfg:             cfg,
		Log:             log,
		CP:              NewControlPlaneClient(cfg, log),
		ScaleToZeroIdle: wcfg.ScaleToZeroIdle,
	}
	log.Info("harness configured",
		"controlplane_http", cfg.ControlPlaneHTTP,
		"ingress_http", cfg.IngressHTTP,
		"ingress_grpc_proxy", cfg.IngressGRPCProxy,
		"worker_grpc", cfg.WorkerGRPC,
		"worker_count", len(cfg.WorkerGRPCs),
		"scale_to_zero_idle", wcfg.ScaleToZeroIdle,
	)
	return h
}

func EchoFunction(userID uint64, image, protocol string, scaleToZeroIdle time.Duration) *core.FunctionSpec {
	maxCC := uint64(0)
	if v := os.Getenv("HYPERFAAS_DST_MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			maxCC = n
		}
	}
	maxInst := uint64(5)
	if v := os.Getenv("HYPERFAAS_DST_MAX_INSTANCES"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			maxInst = n
		}
	}
	spec := EchoFunctionWithScale(userID, image, protocol, scaleToZeroIdle, maxCC, maxInst)
	if v := os.Getenv("HYPERFAAS_DST_MAX_QUEUE_DEPTH"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			spec.Scale.MaxQueueDepth = n
		}
	}
	return spec
}

func EchoFunctionWithConcurrency(userID uint64, image, protocol string, scaleToZeroIdle time.Duration, maxConcurrency uint64) *core.FunctionSpec {
	return EchoFunctionWithScale(userID, image, protocol, scaleToZeroIdle, maxConcurrency, 5)
}

func EchoFunctionWithScale(userID uint64, image, protocol string, scaleToZeroIdle time.Duration, maxConcurrency, maxInstances uint64) *core.FunctionSpec {
	return &core.FunctionSpec{
		UserId: userID,
		Runtime: &core.RuntimeSpec{
			Image:     image,
			Protocol:  protocol,
			Isolation: core.IsolationKind_ISOLATION_KIND_FAKE,
		},
		Scale: &core.ScalePolicySpec{
			MinInstances:           0,
			MaxInstances:           maxInstances,
			MaxConcurrency:         maxConcurrency,
			ScaleToZeroIdleTimeout: durationpb.New(scaleToZeroIdle),
			ColdStartTimeout:       durationpb.New(45 * time.Second),
			RequestTimeout:         durationpb.New(30 * time.Second),
		},
	}
}
