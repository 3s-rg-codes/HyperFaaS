package containerd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/go-cni"
)

// fakeCNI records namespace removals. It embeds cni.CNI so it only needs to
// implement the methods teardown actually calls.
type fakeCNI struct {
	cni.CNI

	mu      sync.Mutex
	removed []removedNamespace
}

type removedNamespace struct {
	id   string
	path string
}

func (c *fakeCNI) Remove(_ context.Context, id, path string, _ ...cni.NamespaceOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removed = append(c.removed, removedNamespace{id: id, path: path})
	return nil
}

func (c *fakeCNI) removedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.removed))
	for _, r := range c.removed {
		out = append(out, r.id)
	}
	return out
}

// fakeContainer and fakeTask embed the containerd interfaces so a test fake
// only needs the methods used during teardown.
type fakeContainer struct {
	containerd.Container

	id        string
	deleteErr error
}

func (c *fakeContainer) ID() string { return c.id }

func (c *fakeContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	return c.deleteErr
}

type fakeTask struct {
	containerd.Task

	deleteErr error
}

func (t *fakeTask) Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error {
	return nil
}

func (t *fakeTask) Delete(context.Context, ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	return nil, t.deleteErr
}

func newTestRuntime(usePool bool) (*Runtime, *fakeCNI) {
	fake := &fakeCNI{}
	rt := &Runtime{
		cfg:         Config{UsePool: usePool, Namespace: "test"},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		cniClient:   fake,
		containers:  make(map[uint64]*ContainerMetadata),
		deleteNetns: func(string) error { return nil },
	}
	if usePool {
		rt.pool = make(chan *pooledNetns, 2)
	}
	rt.teardownCond = sync.NewCond(&rt.teardownMu)
	return rt, fake
}

// TestStopReleasesNetworkWhenContainerAlreadyGone covers the failure mode seen
// on CloudLab: the containerd task/container is already absent, yet teardown
// must still remove the CNI network and drop the runtime metadata instead of
// returning early and leaking the namespace.
func TestStopReleasesNetworkWhenContainerAlreadyGone(t *testing.T) {
	rt, fake := newTestRuntime(false)
	rt.containers[7] = &ContainerMetadata{
		Container: &fakeContainer{id: "hyperfaas-7", deleteErr: errors.New("container does not exist")},
		Task:      &fakeTask{deleteErr: errors.New("task does not exist")},
		HostPort:  portPoolBase,
		GuestIP:   "100.23.0.2",
		NetNs:     "/proc/1/ns/net",
	}

	if err := rt.Stop(context.Background(), 7); err != nil {
		t.Fatalf("Stop returned error for already-gone container: %v", err)
	}
	if got := fake.removedIDs(); len(got) != 1 || got[0] != "hyperfaas-7" {
		t.Fatalf("CNI removals = %v, want [hyperfaas-7]", got)
	}
	if _, ok := rt.containers[7]; ok {
		t.Fatal("container metadata was not removed")
	}
}

// TestStopMissingContainerIsIdempotent ensures a stop for a sandbox already
// reclaimed by the lifecycle watcher succeeds instead of logging the
// "container not found" failure and skipping cleanup.
func TestStopMissingContainerIsIdempotent(t *testing.T) {
	rt, fake := newTestRuntime(false)

	if err := rt.Stop(context.Background(), 999); err != nil {
		t.Fatalf("Stop missing container = %v, want nil", err)
	}
	if got := fake.removedIDs(); len(got) != 0 {
		t.Fatalf("unexpected CNI removals for missing container: %v", got)
	}
}

// TestStopReturnsPooledNetnsToPool verifies the pool accounting on a clean stop.
func TestStopReturnsPooledNetnsToPool(t *testing.T) {
	rt, fake := newTestRuntime(true)
	ns := &pooledNetns{Name: "hf-cni-800001", Path: filepath.Join(netnsMountDir, "hf-cni-800001"), IP: "100.23.0.5"}
	rt.containers[11] = &ContainerMetadata{
		Container: &fakeContainer{id: "hyperfaas-11"},
		Task:      &fakeTask{},
		HostPort:  portPoolBase + 1,
		PooledNS:  ns,
	}

	if err := rt.Stop(context.Background(), 11); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case got := <-rt.pool:
		if got != ns {
			t.Fatalf("pooled netns = %+v, want %+v", got, ns)
		}
	default:
		t.Fatal("pooled netns was not returned to the pool")
	}
	if got := fake.removedIDs(); len(got) != 0 {
		t.Fatalf("pooled netns should be reused, not removed: %v", got)
	}
}

// TestReturnNetnsDestroysWhenNoPool ensures a namespace is destroyed rather
// than silently dropped when no pool exists.
func TestReturnNetnsDestroysWhenNoPool(t *testing.T) {
	rt, fake := newTestRuntime(false)
	var destroyed []string
	rt.deleteNetns = func(name string) error {
		destroyed = append(destroyed, name)
		return nil
	}
	ns := &pooledNetns{Name: "hf-cni-800002", Path: filepath.Join(netnsMountDir, "hf-cni-800002")}

	rt.returnNetnsToPool(ns)

	if len(destroyed) != 1 || destroyed[0] != ns.Name {
		t.Fatalf("destroyed namespaces = %v, want [%s]", destroyed, ns.Name)
	}
	if got := fake.removedIDs(); len(got) != 1 || got[0] != ns.Name {
		t.Fatalf("CNI removals = %v, want [%s]", got, ns.Name)
	}
}

// TestReturnNetnsDestroysWhenPoolFull ensures a full pool destroys the
// namespace instead of dropping it, bounding orphan accumulation.
func TestReturnNetnsDestroysWhenPoolFull(t *testing.T) {
	rt, _ := newTestRuntime(true)
	rt.pool <- &pooledNetns{Name: "hf-cni-a"}
	rt.pool <- &pooledNetns{Name: "hf-cni-b"}
	var destroyed []string
	rt.deleteNetns = func(name string) error {
		destroyed = append(destroyed, name)
		return nil
	}

	rt.returnNetnsToPool(&pooledNetns{Name: "hf-cni-c"})

	if len(destroyed) != 1 || destroyed[0] != "hf-cni-c" {
		t.Fatalf("destroyed namespaces = %v, want [hf-cni-c]", destroyed)
	}
}

// TestSweepOrphanNetns covers the startup sweep used to clear namespaces left
// behind by a previous worker process.
func TestSweepOrphanNetns(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"hf-cni-1", "hf-cni-2", "other-1"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rt, _ := newTestRuntime(false)
	var destroyed []string
	rt.deleteNetns = func(name string) error {
		destroyed = append(destroyed, name)
		return nil
	}

	removed := rt.sweepOrphanNetns(dir, netnsPrefix, map[string]bool{"hf-cni-2": true})

	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if len(destroyed) != 1 || destroyed[0] != "hf-cni-1" {
		t.Fatalf("destroyed namespaces = %v, want [hf-cni-1]", destroyed)
	}
}
