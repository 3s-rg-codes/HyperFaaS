package shared

import (
	"fmt"
	"maps"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

type ExpectedFunction struct {
	UserID     uint64
	FunctionID uint64
	Image      string
	Protocol   string
	Env        map[string]string
	Revision   uint32
}

func CheckUser(got *core.UserSpec, wantID uint64, wantName string) error {
	if got == nil {
		return fmt.Errorf("expected user, got nil")
	}
	if got.GetUserId() == 0 {
		return fmt.Errorf("user_id must be assigned, got %#v", got)
	}
	if wantID != 0 && got.GetUserId() != wantID {
		return fmt.Errorf("user_id: got %d want %d", got.GetUserId(), wantID)
	}
	if got.GetName() != wantName {
		return fmt.Errorf("user name: got %q want %q", got.GetName(), wantName)
	}
	return nil
}

func CheckFunction(got *core.FunctionSpec, want ExpectedFunction) error {
	if got == nil {
		return fmt.Errorf("expected function, got nil")
	}
	if got.GetUserId() != want.UserID {
		return fmt.Errorf("function user_id: got %d want %d", got.GetUserId(), want.UserID)
	}
	if want.FunctionID != 0 && got.GetFunctionId() != want.FunctionID {
		return fmt.Errorf("function_id: got %d want %d", got.GetFunctionId(), want.FunctionID)
	}
	if got.GetFunctionId() == 0 {
		return fmt.Errorf("function_id must be assigned")
	}
	rt := got.GetRuntime()
	if rt == nil {
		return fmt.Errorf("function.runtime is nil")
	}
	if rt.GetImage() != want.Image {
		return fmt.Errorf("runtime.image: got %q want %q", rt.GetImage(), want.Image)
	}
	if rt.GetProtocol() != want.Protocol {
		return fmt.Errorf("runtime.protocol: got %q want %q", rt.GetProtocol(), want.Protocol)
	}
	if !MapsEqual(rt.GetEnv(), want.Env) {
		return fmt.Errorf("runtime.env: got %v want %v", rt.GetEnv(), want.Env)
	}
	return nil
}

func AssertUser(t *testing.T, got *core.UserSpec, wantID uint64, wantName string) {
	t.Helper()
	if err := CheckUser(got, wantID, wantName); err != nil {
		t.Fatal(err)
	}
}

func AssertFunction(t *testing.T, got *core.FunctionSpec, want ExpectedFunction) {
	t.Helper()
	if err := CheckFunction(got, want); err != nil {
		t.Fatal(err)
	}
}

func MapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func CloneEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func UserName(seed int64, seq uint64) string {
	return fmt.Sprintf("dst-user-%x-%d", seed, seq)
}

func EnvMarker(seed int64, seq uint64, revision uint32) string {
	return fmt.Sprintf("dst-env-%x-%d-%d", seed, seq, revision)
}
