package dataplane

import (
	"context"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
)

type testInstance struct {
	functionID, instanceID uint64
	address, protocol      string
	ready, stopping        bool
	available              uint64
}

func mustPut(t testing.TB, store *Store, inst *testInstance) {
	t.Helper()
	state := &core.InstanceState{
		FunctionId:           inst.functionID,
		InstanceId:           inst.instanceID,
		Address:              inst.address,
		Protocol:             inst.protocol,
		Ready:                inst.ready,
		Stopping:             inst.stopping,
		AvailableConcurrency: inst.available,
		MaxConcurrency:       inst.available,
	}
	if err := store.PutInstance(context.Background(), state); err != nil {
		t.Fatalf("PutInstance: %v", err)
	}
}
