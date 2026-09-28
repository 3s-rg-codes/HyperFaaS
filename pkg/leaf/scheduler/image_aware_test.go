package scheduler

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
)

func TestWorkerHasCachedImageDigestAndRef(t *testing.T) {
	w := &core.WorkerState{
		CachedImages: []*core.CachedImage{
			{Image: "registry.example/app:v1", Digest: "sha256:abc", SizeBytes: 10},
		},
	}
	cases := []struct {
		want string
		ok   bool
	}{
		{"registry.example/app:v1", true},
		{"sha256:abc", true},
		{"registry.example/app@sha256:abc", true},
		{"other:latest", false},
	}
	for _, tc := range cases {
		if got := WorkerHasCachedImage(w, tc.want); got != tc.ok {
			t.Fatalf("want %q: got %v want %v", tc.want, got, tc.ok)
		}
	}
}

func TestImageAwarePrefersCachedImage(t *testing.T) {
	sched := NewImageAware(0)
	fn := &core.FunctionSpec{FunctionId: 1, Runtime: &core.RuntimeSpec{Image: "img-a@sha256:deadbeef"}}
	workers := []*core.WorkerState{
		{WorkerId: 1, Healthy: true, Schedulable: true, Capacity: &core.ResourceSpec{CpuUnits: 100}, Allocated: &core.ResourceUsage{}},
		{
			WorkerId: 2, Healthy: true, Schedulable: true, Capacity: &core.ResourceSpec{CpuUnits: 100}, Allocated: &core.ResourceUsage{},
			CachedImages: []*core.CachedImage{{Image: "img-a", Digest: "sha256:deadbeef", CachedAt: timestamppb.Now()}},
		},
		{WorkerId: 3, Healthy: true, Schedulable: true, Capacity: &core.ResourceSpec{CpuUnits: 100}, Allocated: &core.ResourceUsage{}},
	}
	d, err := sched.PickWorker(context.Background(), fn, workers, &core.ScaleDemand{FunctionId: 1, DesiredInstances: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.GetWorkerId() != 2 {
		t.Fatalf("want worker 2 (cache hit), got %d reason=%s", d.GetWorkerId(), d.GetReason())
	}
	if d.GetReason() != "image-aware-hit" {
		t.Fatalf("reason=%q", d.GetReason())
	}
}
