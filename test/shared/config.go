package shared

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config points at a locally deployed hyperfaas-ideal-arch cluster.
// Override any field with the corresponding HYPERFAAS_* environment variable.
// HYPERFAAS_WORKER_GRPC may be a single address or a comma-separated list (multi-worker clusters).
type Config struct {
	ControlPlaneHTTP string
	ControlPlaneGRPC string
	IngressHTTP      string
	IngressGRPCProxy string
	WorkerGRPC       string   // first / primary worker (compat)
	WorkerGRPCs      []string // all workers for scale-to-zero checks
	LeafGRPC         string
	LeafGRPCs        []string // all leaves (fake cluster)
}

// WorkloadConfig tunes the full-stack DST scenario.
type WorkloadConfig struct {
	Users                   int
	FunctionsPerUser        int
	FunctionsUpdatedPerUser int
	InvokesPerFunction      int
	WorkloadDuration        time.Duration
	QuiesceDuration         time.Duration
	ScaleToZeroIdle         time.Duration
	Seed                    int64
	HTTPImage               string // even function index; default echo-http (use sleep-http for slow invokes)
	GRPCImage               string // odd function index; default echo-grpc
}

// PerfConfig tunes the perf benchmark scenarios.
type PerfConfig struct {
	Duration            time.Duration
	HighLoadConcurrency int
	LowLoadConcurrency  int
	MaxFailureRate      float64
}

const (
	defaultUsers                   = 5
	defaultFunctionsPerUser        = 3
	defaultFunctionsUpdatedPerUser = 2
	defaultInvokesPerFunction      = 1500
	defaultWorkloadDuration        = 60 * time.Second
	defaultQuiesceDuration         = 15 * time.Second
	defaultScaleToZeroIdle         = 10 * time.Second
	defaultWorkloadSeed            = 123

	defaultPerfDuration            = 5 * time.Second
	defaultPerfHighLoadConcurrency = 30
	defaultPerfLowLoadConcurrency  = 10
	defaultPerfMaxFailureRate      = 0.05
)

func LoadConfig() Config {
	workers := splitCSV(envOr("HYPERFAAS_WORKER_GRPC", "127.0.0.1:50052"))
	primary := "127.0.0.1:50052"
	if len(workers) > 0 {
		primary = workers[0]
	}
	return Config{
		ControlPlaneHTTP: envOr("HYPERFAAS_CP_HTTP", "127.0.0.1:8081"),
		ControlPlaneGRPC: envOr("HYPERFAAS_CP_GRPC", "127.0.0.1:50054"),
		IngressHTTP:      envOr("HYPERFAAS_INGRESS_HTTP", "127.0.0.1:8080"),
		IngressGRPCProxy: envOr("HYPERFAAS_INGRESS_GRPC_PROXY", "127.0.0.1:50055"),
		WorkerGRPC:       primary,
		WorkerGRPCs:      workers,
		LeafGRPC:         envOr("HYPERFAAS_LEAF_GRPC", "127.0.0.1:50050"),
		LeafGRPCs:        splitCSV(envOr("HYPERFAAS_LEAF_GRPCs", envOr("HYPERFAAS_LEAF_GRPC", "127.0.0.1:50050"))),
	}
}

// WorkerLeafIDs returns leaf IDs parallel to Config.WorkerGRPCs when the fake
// cluster exported HYPERFAAS_FAKE_WORKER_LEAF_IDS.
func WorkerLeafIDs(cfg Config) []uint64 {
	raw := splitCSV(os.Getenv("HYPERFAAS_FAKE_WORKER_LEAF_IDS"))
	if len(raw) == 0 || len(raw) != len(cfg.WorkerGRPCs) {
		return nil
	}
	out := make([]uint64, len(raw))
	for i, s := range raw {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func LoadWorkloadConfig() WorkloadConfig {
	users := envIntOr("HYPERFAAS_DST_USERS", defaultUsers)
	fnsPerUser := envIntOr("HYPERFAAS_DST_FUNCTIONS_PER_USER", defaultFunctionsPerUser)
	updated := envIntOr("HYPERFAAS_DST_FUNCTIONS_UPDATED", defaultFunctionsUpdatedPerUser)
	if updated > fnsPerUser {
		updated = fnsPerUser
	}
	duration := envDurationOr("HYPERFAAS_DST_WORKLOAD_DURATION", defaultWorkloadDuration)
	invokes := envIntOr("HYPERFAAS_DST_INVOKES_PER_FUNCTION", defaultInvokesPerFunction)
	// HYPERFAAS_DST_RPS sizes the plan for ~RPS average over the workload window
	// (uniform random offsets → expected rate ≈ total_invokes / duration).
	if rps := envIntOr("HYPERFAAS_DST_RPS", 0); rps > 0 {
		secs := int(duration / time.Second)
		if secs < 1 {
			secs = 1
		}
		totalFns := users * fnsPerUser
		if totalFns < 1 {
			totalFns = 1
		}
		totalInvokes := rps * secs
		invokes = (totalInvokes + totalFns - 1) / totalFns
		if invokes < 1 {
			invokes = 1
		}
	}
	return WorkloadConfig{
		Users:                   users,
		FunctionsPerUser:        fnsPerUser,
		FunctionsUpdatedPerUser: updated,
		InvokesPerFunction:      invokes,
		WorkloadDuration:        duration,
		QuiesceDuration:         envDurationOr("HYPERFAAS_DST_QUIESCE_DURATION", defaultQuiesceDuration),
		ScaleToZeroIdle:         envDurationOr("HYPERFAAS_DST_SCALE_TO_ZERO", defaultScaleToZeroIdle),
		Seed:                    envInt64Or("HYPERFAAS_DST_SEED", defaultWorkloadSeed),
		HTTPImage:               envOr("HYPERFAAS_DST_HTTP_IMAGE", EchoHTTPImage),
		GRPCImage:               envOr("HYPERFAAS_DST_GRPC_IMAGE", EchoGRPCImage),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envInt64Or(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func LoadPerfConfig() PerfConfig {
	return PerfConfig{
		Duration:            envDurationOr("HYPERFAAS_PERF_DURATION", defaultPerfDuration),
		HighLoadConcurrency: envIntOr("HYPERFAAS_PERF_HIGH_LOAD_CONCURRENCY", defaultPerfHighLoadConcurrency),
		LowLoadConcurrency:  envIntOr("HYPERFAAS_PERF_LOW_LOAD_CONCURRENCY", defaultPerfLowLoadConcurrency),
		MaxFailureRate:      envFloatOr("HYPERFAAS_PERF_MAX_FAILURE_RATE", defaultPerfMaxFailureRate),
	}
}

func envFloatOr(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
