package dst

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

type workloadState struct {
	mu sync.Mutex

	usersReady map[UserKey]chan struct{}
	userID     map[UserKey]uint64
	userGone   map[UserKey]bool

	fnReady map[FnKey]chan struct{}
	fnID    map[FnKey]uint64
	fnOwner map[FnKey]UserKey
	fnGone  map[FnKey]bool
	fnSpec  map[FnKey]shared.ExpectedFunction
}

func newWorkloadState(cfg shared.WorkloadConfig) *workloadState {
	s := &workloadState{
		usersReady: make(map[UserKey]chan struct{}),
		userID:     make(map[UserKey]uint64),
		userGone:   make(map[UserKey]bool),
		fnReady:    make(map[FnKey]chan struct{}),
		fnID:       make(map[FnKey]uint64),
		fnOwner:    make(map[FnKey]UserKey),
		fnGone:     make(map[FnKey]bool),
		fnSpec:     make(map[FnKey]shared.ExpectedFunction),
	}
	for u := range cfg.Users {
		s.usersReady[UserKey(u)] = make(chan struct{})
	}
	for u := range cfg.Users {
		for f := range cfg.FunctionsPerUser {
			key := fnKeyFor(u, f)
			s.fnReady[key] = make(chan struct{})
		}
	}
	return s
}

// RunWorkload executes the concurrent workload, quiesces, verifies scale-to-zero, then deletes all resources.
func RunWorkload(t *testing.T, h *shared.Harness, plan *WorkloadPlan, cfg shared.WorkloadConfig) {
	t.Helper()

	state := newWorkloadState(cfg)
	timeout := cfg.WorkloadDuration + cfg.QuiesceDuration + 5*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var opErrors atomic.Uint64
	workloadStart := time.Now()
	var wg sync.WaitGroup

	for i, op := range plan.Ops {
		wg.Add(1)
		go func(idx int, op WorkloadOp) {
			defer wg.Done()
			if err := waitUntil(ctx, workloadStart.Add(op.At)); err != nil {
				opErrors.Add(1)
				t.Errorf("op %d wait schedule: %v", idx, err)
				return
			}
			if err := executeWorkloadOp(ctx, h, state, op, cfg); err != nil {
				opErrors.Add(1)
				t.Errorf("op %d kind=%d user=%d fn=%d: %v", idx, op.Kind, op.UserKey, op.FnKey, err)
			}
		}(i, op)
	}

	wg.Wait()
	if opErrors.Load() > 0 {
		t.Fatalf("%d workload operations failed", opErrors.Load())
	}

	h.Log.Info("workload phase finished, quiescing for scale-to-zero",
		"quiesce", cfg.QuiesceDuration,
		"scale_to_zero_idle", cfg.ScaleToZeroIdle,
	)

	select {
	case <-ctx.Done():
		t.Fatalf("context ended before quiesce: %v", ctx.Err())
	case <-time.After(cfg.QuiesceDuration):
	}

	fnIDs := state.allFunctionIDs()
	idSet := make(map[uint64]struct{}, len(fnIDs))
	for _, id := range fnIDs {
		idSet[id] = struct{}{}
	}

	workerAddrs := h.Cfg.WorkerGRPCs
	if len(workerAddrs) == 0 {
		workerAddrs = []string{h.Cfg.WorkerGRPC}
	}
	listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer listCancel()
	running, err := shared.RunningInstancesAcrossWorkers(listCtx, workerAddrs, idSet)
	if err != nil {
		t.Fatalf("list worker sandboxes: %v", err)
	}
	if len(running) > 0 {
		t.Fatalf("expected no running instances after quiesce, got %d: %s", len(running), shared.FormatRunning(running))
	}
	h.Log.Info("scale-to-zero verified", "functions", len(fnIDs), "workers", len(workerAddrs))

	if err := cleanupAll(ctx, h, state, cfg); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	h.Log.Info("dst workload complete",
		"users", cfg.Users,
		"functions", cfg.Users*cfg.FunctionsPerUser,
		"invokes", plan.Summary.Invokes,
	)
}

func waitUntil(ctx context.Context, at time.Time) error {
	delay := time.Until(at)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func executeWorkloadOp(ctx context.Context, h *shared.Harness, rs *workloadState, op WorkloadOp, cfg shared.WorkloadConfig) error {
	switch op.Kind {
	case OpCreateUser:
		return execWorkloadCreateUser(ctx, h.CP, rs, op)
	case OpCreateFunction:
		return execWorkloadCreateFunction(ctx, h, rs, op)
	case OpUpdateFunction:
		return execWorkloadUpdateFunction(ctx, h, rs, op)
	case OpInvoke:
		return execWorkloadInvoke(ctx, h, rs, op)
	default:
		return fmt.Errorf("unknown op kind %d", op.Kind)
	}
}

func execWorkloadCreateUser(ctx context.Context, cp *shared.ControlPlaneClient, rs *workloadState, op WorkloadOp) error {
	got, err := cp.CreateUser(ctx, op.Name)
	if err != nil {
		return err
	}
	if err := shared.CheckUser(got, 0, op.Name); err != nil {
		return err
	}
	rs.mu.Lock()
	rs.userID[op.UserKey] = got.GetUserId()
	close(rs.usersReady[op.UserKey])
	rs.mu.Unlock()
	return nil
}

func execWorkloadCreateFunction(ctx context.Context, h *shared.Harness, rs *workloadState, op WorkloadOp) error {
	userID, err := rs.waitUser(ctx, op.UserKey)
	if err != nil {
		return err
	}
	spec := shared.EchoFunction(userID, op.Image, op.Protocol, h.ScaleToZeroIdle)
	spec.Runtime.Env = shared.CloneEnv(op.Env)
	got, err := h.CP.CreateFunction(ctx, userID, spec)
	if err != nil {
		return err
	}
	want := shared.ExpectedFunction{
		UserID:   userID,
		Image:    op.Image,
		Protocol: op.Protocol,
		Env:      shared.CloneEnv(op.Env),
	}
	if err := shared.CheckFunction(got, want); err != nil {
		return err
	}
	want.FunctionID = got.GetFunctionId()
	rs.mu.Lock()
	rs.fnID[op.FnKey] = got.GetFunctionId()
	rs.fnOwner[op.FnKey] = op.UserKey
	rs.fnSpec[op.FnKey] = want
	close(rs.fnReady[op.FnKey])
	rs.mu.Unlock()
	return nil
}

func execWorkloadUpdateFunction(ctx context.Context, h *shared.Harness, rs *workloadState, op WorkloadOp) error {
	userID, fnID, cur, err := rs.waitFunction(ctx, op.FnKey)
	if err != nil {
		return err
	}
	spec := shared.EchoFunction(userID, cur.Image, cur.Protocol, h.ScaleToZeroIdle)
	spec.FunctionId = fnID
	spec.Runtime.Env = shared.CloneEnv(op.Env)
	got, err := h.CP.UpdateFunction(ctx, userID, spec)
	if err != nil {
		return err
	}
	want := cur
	want.Env = shared.CloneEnv(op.Env)
	want.FunctionID = fnID
	if err := shared.CheckFunction(got, want); err != nil {
		return err
	}
	rs.mu.Lock()
	rs.fnSpec[op.FnKey] = want
	rs.mu.Unlock()
	return nil
}

func execWorkloadInvoke(ctx context.Context, h *shared.Harness, rs *workloadState, op WorkloadOp) error {
	userID, fnID, spec, err := rs.waitFunction(ctx, op.FnKey)
	if err != nil {
		return err
	}
	invokeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var body []byte
	switch spec.Protocol {
	case "grpc":
		body, err = shared.InvokeGRPC(invokeCtx, h.Cfg.IngressGRPCProxy, userID, fnID, op.Payload)
		if err != nil {
			return fmt.Errorf("grpc invoke: %w", err)
		}
	default:
		var status int
		body, status, err = shared.InvokeHTTP(invokeCtx, h.Cfg.IngressHTTP, userID, fnID, op.Payload)
		if err != nil {
			return fmt.Errorf("http invoke: %w", err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("http invoke status=%d body=%q", status, body)
		}
	}
	if string(body) != string(op.Payload) {
		return fmt.Errorf("echo mismatch fn=%d: got %q want %q", fnID, body, op.Payload)
	}
	return nil
}

func cleanupAll(ctx context.Context, h *shared.Harness, rs *workloadState, cfg shared.WorkloadConfig) error {
	var wg sync.WaitGroup
	var firstErr atomic.Value

	deleteFn := func(userKey UserKey, fnKey FnKey) {
		defer wg.Done()
		userID, fnID, _, err := rs.waitFunction(ctx, fnKey)
		if err != nil {
			return
		}
		if err := h.CP.DeleteFunction(ctx, userID, fnID); err != nil {
			firstErr.CompareAndSwap(nil, err)
			return
		}
		rs.mu.Lock()
		rs.fnGone[fnKey] = true
		rs.mu.Unlock()
	}

	for u := range cfg.Users {
		for f := range cfg.FunctionsPerUser {
			wg.Add(1)
			go deleteFn(UserKey(u), fnKeyFor(u, f))
		}
	}
	wg.Wait()
	if v := firstErr.Load(); v != nil {
		return v.(error)
	}

	for u := range cfg.Users {
		userID, err := rs.waitUser(ctx, UserKey(u))
		if err != nil {
			continue
		}
		if err := h.CP.DeleteUser(ctx, userID); err != nil {
			return err
		}
		rs.mu.Lock()
		rs.userGone[UserKey(u)] = true
		rs.mu.Unlock()
	}
	return nil
}

func (rs *workloadState) waitUser(ctx context.Context, key UserKey) (uint64, error) {
	rs.mu.Lock()
	ch, ok := rs.usersReady[key]
	gone := rs.userGone[key]
	rs.mu.Unlock()
	if gone {
		return 0, fmt.Errorf("user %d deleted", key)
	}
	if !ok {
		return 0, fmt.Errorf("user %d not in plan", key)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-ch:
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.userGone[key] {
		return 0, fmt.Errorf("user %d deleted", key)
	}
	return rs.userID[key], nil
}

func (rs *workloadState) waitFunction(ctx context.Context, key FnKey) (userID, fnID uint64, spec shared.ExpectedFunction, err error) {
	rs.mu.Lock()
	ch, ok := rs.fnReady[key]
	gone := rs.fnGone[key]
	rs.mu.Unlock()
	if gone {
		return 0, 0, shared.ExpectedFunction{}, fmt.Errorf("function %d deleted", key)
	}
	if !ok {
		return 0, 0, shared.ExpectedFunction{}, fmt.Errorf("function %d not in plan", key)
	}
	select {
	case <-ctx.Done():
		return 0, 0, shared.ExpectedFunction{}, ctx.Err()
	case <-ch:
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.fnGone[key] {
		return 0, 0, shared.ExpectedFunction{}, fmt.Errorf("function %d deleted", key)
	}
	owner := rs.fnOwner[key]
	if rs.userGone[owner] {
		return 0, 0, shared.ExpectedFunction{}, fmt.Errorf("function %d owner deleted", key)
	}
	return rs.userID[owner], rs.fnID[key], rs.fnSpec[key], nil
}

func (rs *workloadState) allFunctionIDs() []uint64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]uint64, 0, len(rs.fnID))
	for _, id := range rs.fnID {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}
