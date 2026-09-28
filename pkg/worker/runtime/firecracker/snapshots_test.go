package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotManagerInvalidateRemovesFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := newSnapshotManager(dir)
	paths := manager.paths(7)
	if err := os.WriteFile(paths.MemoryPath, []byte("mem"), 0o600); err != nil {
		t.Fatalf("write memory file: %v", err)
	}
	if err := os.WriteFile(paths.SnapshotPath, []byte("snap"), 0o600); err != nil {
		t.Fatalf("write snapshot file: %v", err)
	}
	paths.RootfsPath = filepath.Join(dir, "rootfs.ext4")
	manager.put("7", paths)

	manager.invalidate(7)

	if _, ok := manager.get("7"); ok {
		t.Fatal("snapshot metadata should be removed")
	}
	for _, path := range []string{paths.MemoryPath, paths.SnapshotPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, err=%v", path, err)
		}
	}
}

func TestSnapshotCreateSingleFlight(t *testing.T) {
	t.Parallel()

	manager := newSnapshotManager(t.TempDir())
	job1, creator1 := manager.beginCreate("1")
	if !creator1 || job1 == nil {
		t.Fatalf("first beginCreate should create job, got creator=%v job=%v", creator1, job1)
	}

	job2, creator2 := manager.beginCreate("1")
	if creator2 || job2 != job1 {
		t.Fatalf("second beginCreate should wait on existing job")
	}

	manager.finishCreate("1", job1, nil)
	manager.put("1", manager.paths(1))

	job3, creator3 := manager.beginCreate("1")
	if creator3 || job3 != nil {
		t.Fatalf("beginCreate after snapshot exists should return immediately")
	}
}
