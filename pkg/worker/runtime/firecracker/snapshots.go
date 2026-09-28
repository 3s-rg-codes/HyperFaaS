package firecracker

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type snapshotMetadata struct {
	MemoryPath   string
	SnapshotPath string
	RootfsPath   string
}

type snapshotManager struct {
	dir string

	mu        sync.RWMutex
	snapshots map[string]snapshotMetadata

	createMu   sync.Mutex
	createJobs map[string]*snapshotCreateJob
}

type snapshotCreateJob struct {
	wg  sync.WaitGroup
	err error
}

func newSnapshotManager(dir string) *snapshotManager {
	return &snapshotManager{
		dir:        dir,
		snapshots:  make(map[string]snapshotMetadata),
		createJobs: make(map[string]*snapshotCreateJob),
	}
}

func (m *snapshotManager) get(key string) (snapshotMetadata, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta, ok := m.snapshots[key]
	return meta, ok
}

func (m *snapshotManager) put(key string, meta snapshotMetadata) {
	m.mu.Lock()
	m.snapshots[key] = meta
	m.mu.Unlock()
}

func (m *snapshotManager) paths(functionID uint64) snapshotMetadata {
	name := strconv.FormatUint(functionID, 10)
	return snapshotMetadata{
		MemoryPath:   filepath.Join(m.dir, "memory-"+name),
		SnapshotPath: filepath.Join(m.dir, "snapshot-"+name),
	}
}

func (m *snapshotManager) invalidate(functionID uint64) {
	key := strconv.FormatUint(functionID, 10)

	// Wait for any in-flight creation to finish before deleting its files.
	// Deleting memory-<id>.creating while a creator is publishing makes the
	// rename fail and cascades into repeated cold boots. Re-check after each
	// wait, because a new creator can start in the gap.
	m.createMu.Lock()
	for {
		job := m.createJobs[key]
		if job == nil {
			break
		}
		m.createMu.Unlock()
		_ = m.waitCreate(job)
		m.createMu.Lock()
	}

	paths := m.paths(functionID)
	m.mu.Lock()
	if meta, ok := m.snapshots[key]; ok {
		paths = meta
	}
	delete(m.snapshots, key)
	m.mu.Unlock()

	for _, path := range []string{
		paths.MemoryPath,
		paths.SnapshotPath,
		paths.MemoryPath + ".creating",
		paths.SnapshotPath + ".creating",
	} {
		_ = os.Remove(path)
	}
	m.createMu.Unlock()
}

func (m *snapshotManager) beginCreate(key string) (*snapshotCreateJob, bool) {
	m.createMu.Lock()
	defer m.createMu.Unlock()

	// Lock order is createMu -> m.mu (same as invalidate); never the reverse.
	m.mu.RLock()
	_, exists := m.snapshots[key]
	m.mu.RUnlock()
	if exists {
		return nil, false
	}
	if job, exists := m.createJobs[key]; exists {
		return job, false
	}

	job := &snapshotCreateJob{}
	job.wg.Add(1)
	m.createJobs[key] = job
	return job, true
}

func (m *snapshotManager) finishCreate(key string, job *snapshotCreateJob, err error) {
	m.createMu.Lock()
	job.err = err
	job.wg.Done()
	delete(m.createJobs, key)
	m.createMu.Unlock()
}

func (m *snapshotManager) waitCreate(job *snapshotCreateJob) error {
	job.wg.Wait()
	return job.err
}
