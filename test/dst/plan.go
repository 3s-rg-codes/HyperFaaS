package dst

import (
	"fmt"
	"math/rand/v2"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

type UserKey int
type FnKey int

type WorkloadOpKind uint8

const (
	OpCreateUser WorkloadOpKind = iota
	OpCreateFunction
	OpUpdateFunction
	OpInvoke
)

// WorkloadOp is one action in the concurrent workload phase (no deletes).
type WorkloadOp struct {
	Kind     WorkloadOpKind
	At       time.Duration
	UserKey  UserKey
	FnKey    FnKey
	Name     string
	Image    string
	Protocol string
	Env      map[string]string
	// InvokeIndex is the per-function invoke ordinal (0..M-1).
	InvokeIndex int
	Payload     []byte
}

// WorkloadSummary counts planned workload-phase operations.
type WorkloadSummary struct {
	Users          int
	Functions      int
	Updates        int
	Invokes        int
	WorkloadWindow time.Duration
}

// WorkloadPlan is a deterministic concurrent workload (CRUD + invoke, no deletes).
type WorkloadPlan struct {
	Seed    int64
	Ops     []WorkloadOp
	Summary WorkloadSummary
}

// GenerateWorkloadPlan builds X users, Y functions each, N updates per user, M invokes per function.
// Every op gets a random start offset in [0, cfg.WorkloadDuration); execution order is readiness-driven.
func GenerateWorkloadPlan(cfg shared.WorkloadConfig) *WorkloadPlan {
	rng := rand.New(rand.NewPCG(uint64(cfg.Seed), uint64(cfg.Seed>>1)))
	window := cfg.WorkloadDuration

	ops := make([]WorkloadOp, 0,
		cfg.Users+
			cfg.Users*cfg.FunctionsPerUser+
			cfg.Users*cfg.FunctionsUpdatedPerUser+
			cfg.Users*cfg.FunctionsPerUser*cfg.InvokesPerFunction)

	for u := range cfg.Users {
		ops = append(ops, WorkloadOp{
			Kind:    OpCreateUser,
			At:      randomOffset(rng, window),
			UserKey: UserKey(u),
			Name:    shared.UserName(cfg.Seed, uint64(u)),
		})
	}

	for u := range cfg.Users {
		for f := range cfg.FunctionsPerUser {
			fnKey := fnKeyFor(u, f)
			protocol := "http"
			image := cfg.HTTPImage
			if image == "" {
				image = shared.EchoHTTPImage
			}
			if f%2 == 1 {
				protocol = "grpc"
				image = cfg.GRPCImage
				if image == "" {
					image = shared.EchoGRPCImage
				}
			}
			ops = append(ops, WorkloadOp{
				Kind:     OpCreateFunction,
				At:       randomOffset(rng, window),
				UserKey:  UserKey(u),
				FnKey:    fnKey,
				Image:    image,
				Protocol: protocol,
				Env:      map[string]string{"marker": shared.EnvMarker(cfg.Seed, uint64(fnKey), 0)},
			})
		}
	}

	for u := range cfg.Users {
		for f := range cfg.FunctionsUpdatedPerUser {
			fnKey := fnKeyFor(u, f)
			ops = append(ops, WorkloadOp{
				Kind:    OpUpdateFunction,
				At:      randomOffset(rng, window),
				UserKey: UserKey(u),
				FnKey:   fnKey,
				Env:     map[string]string{"marker": shared.EnvMarker(cfg.Seed, uint64(fnKey), 1)},
			})
		}
	}

	for u := range cfg.Users {
		for f := range cfg.FunctionsPerUser {
			fnKey := fnKeyFor(u, f)
			for m := range cfg.InvokesPerFunction {
				ops = append(ops, WorkloadOp{
					Kind:        OpInvoke,
					At:          randomOffset(rng, window),
					UserKey:     UserKey(u),
					FnKey:       fnKey,
					InvokeIndex: m,
					Payload:     invokePayload(cfg.Seed, UserKey(u), fnKey, m),
				})
			}
		}
	}

	rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })

	return &WorkloadPlan{
		Seed: cfg.Seed,
		Ops:  ops,
		Summary: WorkloadSummary{
			Users:          cfg.Users,
			Functions:      cfg.Users * cfg.FunctionsPerUser,
			Updates:        cfg.Users * cfg.FunctionsUpdatedPerUser,
			Invokes:        cfg.Users * cfg.FunctionsPerUser * cfg.InvokesPerFunction,
			WorkloadWindow: window,
		},
	}
}

func fnKeyFor(userIdx, fnIdx int) FnKey {
	return FnKey(userIdx*1000 + fnIdx)
}

func userIdxFromFnKey(key FnKey) int {
	return int(key) / 1000
}

func fnIdxFromFnKey(key FnKey) int {
	return int(key) % 1000
}

func randomOffset(rng *rand.Rand, window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	return time.Duration(rng.Int64N(int64(window)))
}

func invokePayload(seed int64, user UserKey, fn FnKey, n int) []byte {
	return fmt.Appendf(nil, "dst-invoke-%x-u%d-f%d-%d", seed, user, fn, n)
}
