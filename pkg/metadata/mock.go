package metadata

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

var _ Client = &MockClient{}

// MockClient provides an in-memory metadata store for tests.
type MockClient struct {
	mu        sync.RWMutex
	functions map[string]*FunctionMetadata
	watchers  map[int]*mockWatcher

	revision      int64
	nextWatcherID int
}

type mockWatcher struct {
	ctx          context.Context
	events       chan Event
	errs         chan error
	lastRevision int64
}

// NewMockClient initialises an empty mock metadata client.
func NewMockClient() *MockClient {
	return &MockClient{
		functions: make(map[string]*FunctionMetadata),
		watchers:  make(map[int]*mockWatcher),
	}
}

// PutFunction stores new function metadata and generates a function ID.
func (m *MockClient) PutFunction(ctx context.Context, req *common.CreateFunctionRequest) (string, error) {
	if req == nil {
		return "", ErrReqIsNil
	}
	id := uuid.NewString()
	if err := m.PutFunctionWithID(ctx, id, req); err != nil {
		return "", err
	}
	return id, nil
}

// PutFunctionWithID stores metadata for the provided ID.
func (m *MockClient) PutFunctionWithID(_ context.Context, id string, req *common.CreateFunctionRequest) error {
	if id == "" {
		return ErrFunctionIDIsEmpty
	}
	if req == nil {
		return ErrReqIsNil
	}

	meta := newFunctionMetadataFromRequest(id, req)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.functions[id]; exists {
		return ErrFunctionAlreadyExists
	}

	m.functions[id] = meta
	m.broadcastLocked(Event{Type: EventTypePut, FunctionID: id, Function: cloneFunctionMetadata(meta)})
	return nil
}

// UpdateFunction replaces metadata for an existing function.
func (m *MockClient) UpdateFunction(_ context.Context, id string, req *common.CreateFunctionRequest) error {
	if id == "" {
		return ErrFunctionIDIsEmpty
	}
	if req == nil {
		return ErrReqIsNil
	}

	meta := newFunctionMetadataFromRequest(id, req)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.functions[id]; !exists {
		return ErrFunctionNotFound
	}

	m.functions[id] = meta
	m.broadcastLocked(Event{Type: EventTypePut, FunctionID: id, Function: cloneFunctionMetadata(meta)})
	return nil
}

// DeleteFunction removes metadata for the given function ID.
func (m *MockClient) DeleteFunction(_ context.Context, id string) error {
	if id == "" {
		return ErrFunctionIDIsEmpty
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.functions[id]; !exists {
		return ErrFunctionNotFound
	}
	delete(m.functions, id)
	m.broadcastLocked(Event{Type: EventTypeDelete, FunctionID: id})
	return nil
}

// GetFunction returns the metadata for the given function ID.
func (m *MockClient) GetFunction(_ context.Context, id string) (*FunctionMetadata, error) {
	if id == "" {
		return nil, ErrFunctionIDIsEmpty
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	meta, ok := m.functions[id]
	if !ok {
		return nil, ErrFunctionNotFound
	}
	return cloneFunctionMetadata(meta), nil
}

// ListFunctions returns a snapshot of all stored metadata.
func (m *MockClient) ListFunctions(_ context.Context) (*ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	functions := make([]*FunctionMetadata, 0, len(m.functions))
	for _, meta := range m.functions {
		functions = append(functions, cloneFunctionMetadata(meta))
	}
	return &ListResult{Functions: functions, Revision: m.revision}, nil
}

// WatchFunctions streams metadata changes starting after the provided revision.
func (m *MockClient) WatchFunctions(ctx context.Context, revision int64) (<-chan Event, <-chan error) {
	events := make(chan Event, 64)
	errs := make(chan error, 1)

	watcher := &mockWatcher{
		ctx:          ctx,
		events:       events,
		errs:         errs,
		lastRevision: revision,
	}

	m.mu.Lock()
	id := m.nextWatcherID
	m.nextWatcherID++
	m.watchers[id] = watcher
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		delete(m.watchers, id)
		m.mu.Unlock()
		close(events)
		close(errs)
	}()

	return events, errs
}

// Close releases resources and stops active watchers.
func (m *MockClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, watcher := range m.watchers {
		close(watcher.events)
		close(watcher.errs)
		delete(m.watchers, id)
	}
	return nil
}

func (m *MockClient) broadcastLocked(ev Event) {
	m.revision++
	for id, watcher := range m.watchers {
		if m.revision <= watcher.lastRevision {
			continue
		}
		select {
		case <-watcher.ctx.Done():
			close(watcher.events)
			close(watcher.errs)
			delete(m.watchers, id)
			continue
		default:
		}

		select {
		case watcher.events <- ev:
			watcher.lastRevision = m.revision
		case <-watcher.ctx.Done():
			close(watcher.events)
			close(watcher.errs)
			delete(m.watchers, id)
		}
	}
}

func newFunctionMetadataFromRequest(id string, req *common.CreateFunctionRequest) *FunctionMetadata {
	meta := &FunctionMetadata{
		ID: id,
	}
	if req.GetImage() != nil {
		meta.Image = proto.Clone(req.GetImage()).(*common.Image)
	}
	if req.GetConfig() != nil {
		meta.Config = proto.Clone(req.GetConfig()).(*common.Config)
	}
	return meta
}

func cloneFunctionMetadata(meta *FunctionMetadata) *FunctionMetadata {
	if meta == nil {
		return nil
	}
	cloned := &FunctionMetadata{ID: meta.ID}
	if meta.Image != nil {
		cloned.Image = proto.Clone(meta.Image).(*common.Image)
	}
	if meta.Config != nil {
		cloned.Config = proto.Clone(meta.Config).(*common.Config)
	}
	return cloned
}
