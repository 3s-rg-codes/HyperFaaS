package dst

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"hyperfaas-ideal-arch/test/shared"
)

func TestDSTFullWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full DST in -short mode")
	}

	h := shared.NewHarness(t)
	wcfg := shared.LoadWorkloadConfig()
	if wcfg.WorkloadDuration < 10*time.Second {
		t.Fatalf("workload duration must be at least 10s, got %s", wcfg.WorkloadDuration)
	}
	if wcfg.FunctionsUpdatedPerUser > wcfg.FunctionsPerUser {
		t.Fatalf("functions updated (%d) cannot exceed functions per user (%d)",
			wcfg.FunctionsUpdatedPerUser, wcfg.FunctionsPerUser)
	}

	plan := GenerateWorkloadPlan(wcfg)
	h.Log.Info("generated workload plan",
		"seed", plan.Seed,
		"users", plan.Summary.Users,
		"functions", plan.Summary.Functions,
		"updates", plan.Summary.Updates,
		"invokes", plan.Summary.Invokes,
		"ops", len(plan.Ops),
		"workload_window", plan.Summary.WorkloadWindow,
		"quiesce", wcfg.QuiesceDuration,
		"scale_to_zero", wcfg.ScaleToZeroIdle,
	)

	RunWorkload(t, h, plan, wcfg)
}

func TestDSTLifecycle(t *testing.T) {
	h := shared.NewHarness(t)
	ctx := context.Background()

	user, err := h.CP.CreateUser(ctx, "alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	echoHTTP, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunction(user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle))
	if err != nil {
		t.Fatalf("create echo-http function: %v", err)
	}
	echoGRPC, err := h.CP.CreateFunction(ctx, user.GetUserId(), shared.EchoFunction(user.GetUserId(), shared.EchoGRPCImage, "grpc", h.ScaleToZeroIdle))
	if err != nil {
		t.Fatalf("create echo-grpc function: %v", err)
	}

	payload := []byte("deterministic-simulation")

	httpBody, status, err := shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), echoHTTP.GetFunctionId(), payload)
	if err != nil {
		t.Fatalf("http invoke: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("http invoke status = %d body = %q", status, httpBody)
	}
	if string(httpBody) != string(payload) {
		t.Fatalf("http echo mismatch: got %q want %q", httpBody, payload)
	}

	grpcBody, err := shared.InvokeGRPC(ctx, h.Cfg.IngressGRPCProxy, user.GetUserId(), echoGRPC.GetFunctionId(), payload)
	if err != nil {
		t.Fatalf("grpc invoke: %v", err)
	}
	if string(grpcBody) != string(payload) {
		t.Fatalf("grpc echo mismatch: got %q want %q", grpcBody, payload)
	}

	updated := shared.EchoFunction(user.GetUserId(), shared.EchoHTTPImage, "http", h.ScaleToZeroIdle)
	updated.FunctionId = echoHTTP.GetFunctionId()
	updated.Runtime.Env = map[string]string{"marker": "updated"}
	if _, err := h.CP.UpdateFunction(ctx, user.GetUserId(), updated); err != nil {
		t.Fatalf("update function: %v", err)
	}

	_, status, err = shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), echoHTTP.GetFunctionId(), []byte("after-update"))
	if err != nil {
		t.Fatalf("http invoke after update: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("http invoke after update status = %d", status)
	}

	if err := h.CP.DeleteFunction(ctx, user.GetUserId(), echoHTTP.GetFunctionId()); err != nil {
		t.Fatalf("delete function: %v", err)
	}
	_, status, err = shared.InvokeHTTP(ctx, h.Cfg.IngressHTTP, user.GetUserId(), echoHTTP.GetFunctionId(), payload)
	if err == nil && status == http.StatusOK {
		t.Fatal("expected invoke after delete to fail")
	}
	if !isNotFound(err) && status != http.StatusNotFound && status != http.StatusBadGateway && status != http.StatusNotImplemented {
		t.Fatalf("expected not found after delete, status=%d err=%v", status, err)
	}

	if err := h.CP.DeleteUser(ctx, user.GetUserId()); err != nil {
		t.Fatalf("delete user: %v", err)
	}
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
