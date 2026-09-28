package dataplane

import (
	"context"
	"testing"
)

func BenchmarkSandboxPoolAcquireRelease(b *testing.B) {
	pool := NewSandboxPool(DefaultSandboxConcurrency, 1024)
	pool.Add(Sandbox{InstanceID: 1, Address: "sandbox:1", Protocol: "http"})
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lease, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			lease.Release()
		}
	})
}
