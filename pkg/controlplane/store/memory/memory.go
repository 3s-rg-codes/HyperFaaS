package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
)

type Store struct {
	mu sync.RWMutex

	nextUserID     uint64
	nextFunctionID uint64

	users     map[uint64]*core.UserSpec
	functions map[uint64]map[uint64]*core.FunctionSpec

	watchers []chan *core.FunctionEvent
	closed   bool

	// Dynamic platform configuration. The memory backend assigns versions from
	// its own counter because it has no store revision like etcd.
	configVersion  uint64
	platformConfig *core.PlatformConfig
	configWatchers []*configWatcher
}

// configWatcher is a latest-wins mailbox for one platform-config subscriber.
//
// A plain buffered channel would drop updates when full, which could leave a
// component on a stale policy with no error and no reconnect. Instead the
// watcher always holds the newest pending document and a wakeup tells the watch
// goroutine to deliver it, so the final stored version is never lost and memory
// stays bounded.
type configWatcher struct {
	mu     sync.Mutex
	latest *core.PlatformConfig
	notify chan struct{}
	closed bool
}

func newConfigWatcher() *configWatcher {
	return &configWatcher{notify: make(chan struct{}, 1)}
}

func (w *configWatcher) set(cfg *core.PlatformConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.latest = cfg
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *configWatcher) take() (*core.PlatformConfig, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	cfg := w.latest
	w.latest = nil
	return cfg, !w.closed
}

func (w *configWatcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func New() *Store {
	return &Store{
		nextUserID:     1,
		nextFunctionID: 1,
		users:          make(map[uint64]*core.UserSpec),
		functions:      make(map[uint64]map[uint64]*core.FunctionSpec),
	}
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, ch := range s.watchers {
		close(ch)
	}
	s.watchers = nil
	for _, ch := range s.configWatchers {
		ch.close()
	}
	s.configWatchers = nil
	return nil
}

func (s *Store) CreateUser(_ context.Context, user *core.UserSpec) error {
	if user == nil {
		return status.Error(codes.InvalidArgument, "user is required")
	}
	if user.GetName() == "" {
		return status.Error(codes.InvalidArgument, "user.name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}

	id := user.GetUserId()
	if id == 0 {
		id = s.nextUserID
		s.nextUserID++
		user.UserId = id
	}
	if _, exists := s.users[id]; exists {
		return status.Errorf(codes.AlreadyExists, "user %d already exists", id)
	}
	s.users[id] = cloneUser(user)
	return nil
}

func (s *Store) UpdateUser(_ context.Context, user *core.UserSpec) error {
	if user == nil || user.GetUserId() == 0 {
		return status.Error(codes.InvalidArgument, "user.user_id is required")
	}
	if user.GetName() == "" {
		return status.Error(codes.InvalidArgument, "user.name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}
	if _, exists := s.users[user.GetUserId()]; !exists {
		return status.Errorf(codes.NotFound, "user %d not found", user.GetUserId())
	}
	s.users[user.GetUserId()] = cloneUser(user)
	return nil
}

func (s *Store) DeleteUser(_ context.Context, userID uint64) error {
	if userID == 0 {
		return status.Error(codes.InvalidArgument, "user_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}
	if _, exists := s.users[userID]; !exists {
		return status.Errorf(codes.NotFound, "user %d not found", userID)
	}
	delete(s.users, userID)

	funcs := s.functions[userID]
	delete(s.functions, userID)
	for functionID := range funcs {
		s.emitLocked(&core.FunctionEvent{
			Type:       core.FunctionEventType_FUNCTION_EVENT_TYPE_DELETED,
			Function:   &core.FunctionSpec{UserId: userID, FunctionId: functionID},
			ObservedAt: timestamppb.Now(),
		})
	}
	return nil
}

func (s *Store) GetUser(_ context.Context, userID uint64) (*core.UserSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[userID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %d not found", userID)
	}
	return cloneUser(user), nil
}

func (s *Store) ListUsers(_ context.Context) ([]*core.UserSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*core.UserSpec, 0, len(s.users))
	for _, user := range s.users {
		out = append(out, cloneUser(user))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetUserId() < out[j].GetUserId() })
	return out, nil
}

func (s *Store) CreateFunction(_ context.Context, function *core.FunctionSpec) error {
	if err := validateFunction(function); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}
	if _, ok := s.users[function.GetUserId()]; !ok {
		return status.Errorf(codes.NotFound, "user %d not found", function.GetUserId())
	}

	functionID := function.GetFunctionId()
	if functionID == 0 {
		functionID = s.nextFunctionID
		s.nextFunctionID++
		function.FunctionId = functionID
	}
	if s.functions[function.GetUserId()] == nil {
		s.functions[function.GetUserId()] = make(map[uint64]*core.FunctionSpec)
	}
	if _, exists := s.functions[function.GetUserId()][functionID]; exists {
		return status.Errorf(codes.AlreadyExists, "function %d/%d already exists", function.GetUserId(), functionID)
	}

	stored := cloneFunction(function)
	s.functions[function.GetUserId()][functionID] = stored
	s.emitLocked(&core.FunctionEvent{
		Type:       core.FunctionEventType_FUNCTION_EVENT_TYPE_CREATED,
		Function:   cloneFunction(stored),
		ObservedAt: timestamppb.Now(),
	})
	return nil
}

func (s *Store) UpdateFunction(_ context.Context, function *core.FunctionSpec) error {
	if err := validateFunction(function); err != nil {
		return err
	}
	if function.GetFunctionId() == 0 {
		return status.Error(codes.InvalidArgument, "function.function_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}
	userFuncs, ok := s.functions[function.GetUserId()]
	if !ok {
		return status.Errorf(codes.NotFound, "function %d/%d not found", function.GetUserId(), function.GetFunctionId())
	}
	if _, ok := userFuncs[function.GetFunctionId()]; !ok {
		return status.Errorf(codes.NotFound, "function %d/%d not found", function.GetUserId(), function.GetFunctionId())
	}

	stored := cloneFunction(function)
	userFuncs[function.GetFunctionId()] = stored
	s.emitLocked(&core.FunctionEvent{
		Type:       core.FunctionEventType_FUNCTION_EVENT_TYPE_UPDATED,
		Function:   cloneFunction(stored),
		ObservedAt: timestamppb.Now(),
	})
	return nil
}

func (s *Store) DeleteFunction(_ context.Context, userID, functionID uint64) error {
	if userID == 0 || functionID == 0 {
		return status.Error(codes.InvalidArgument, "user_id and function_id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return status.Error(codes.FailedPrecondition, "store is closed")
	}
	userFuncs, ok := s.functions[userID]
	if !ok {
		return status.Errorf(codes.NotFound, "function %d/%d not found", userID, functionID)
	}
	if _, ok := userFuncs[functionID]; !ok {
		return status.Errorf(codes.NotFound, "function %d/%d not found", userID, functionID)
	}
	delete(userFuncs, functionID)
	if len(userFuncs) == 0 {
		delete(s.functions, userID)
	}
	s.emitLocked(&core.FunctionEvent{
		Type:       core.FunctionEventType_FUNCTION_EVENT_TYPE_DELETED,
		Function:   &core.FunctionSpec{UserId: userID, FunctionId: functionID},
		ObservedAt: timestamppb.Now(),
	})
	return nil
}

func (s *Store) GetFunction(_ context.Context, userID, functionID uint64) (*core.FunctionSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn, ok := s.functions[userID][functionID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "function %d/%d not found", userID, functionID)
	}
	return cloneFunction(fn), nil
}

func (s *Store) ListFunctions(_ context.Context, userID uint64) ([]*core.FunctionSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	userFuncs := s.functions[userID]
	out := make([]*core.FunctionSpec, 0, len(userFuncs))
	for _, fn := range userFuncs {
		out = append(out, cloneFunction(fn))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetFunctionId() < out[j].GetFunctionId() })
	return out, nil
}

func (s *Store) WatchFunctions(_ context.Context) (<-chan *core.FunctionEvent, <-chan error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("store is closed")
		close(errCh)
		return nil, errCh
	}
	ch := make(chan *core.FunctionEvent, 16)
	s.watchers = append(s.watchers, ch)
	return ch, nil
}

func (s *Store) emitLocked(event *core.FunctionEvent) {
	for _, ch := range s.watchers {
		select {
		case ch <- cloneEvent(event):
		default:
		}
	}
}

func (s *Store) GetPlatformConfig(_ context.Context) (*core.PlatformConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.platformConfig == nil {
		return nil, status.Error(codes.NotFound, "platform config not set")
	}
	return proto.Clone(s.platformConfig).(*core.PlatformConfig), nil
}

func (s *Store) PutPlatformConfig(_ context.Context, cfg *core.PlatformConfig, expectedVersion uint64) (*core.PlatformConfig, error) {
	if cfg == nil {
		return nil, status.Error(codes.InvalidArgument, "platform config is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, status.Error(codes.FailedPrecondition, "store is closed")
	}
	if expectedVersion != 0 && expectedVersion != s.configVersion {
		return nil, status.Errorf(codes.Aborted, "platform config version changed: expected %d, current %d", expectedVersion, s.configVersion)
	}
	s.configVersion++
	stored := proto.Clone(cfg).(*core.PlatformConfig)
	stored.Version = s.configVersion
	s.platformConfig = stored
	s.emitConfigLocked(stored)
	return proto.Clone(stored).(*core.PlatformConfig), nil
}

func (s *Store) WatchPlatformConfig(ctx context.Context) (<-chan *core.PlatformConfig, <-chan error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("store is closed")
		close(errCh)
		return nil, errCh
	}
	w := newConfigWatcher()
	// Seed the watcher with the current document so a subscriber gets the
	// initial value from the watch itself and needs no separate Get.
	if s.platformConfig != nil {
		w.set(proto.Clone(s.platformConfig).(*core.PlatformConfig))
	}
	s.configWatchers = append(s.configWatchers, w)
	s.mu.Unlock()

	out := make(chan *core.PlatformConfig, 8)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.notify:
			}
			cfg, ok := w.take()
			if !ok {
				return
			}
			if cfg == nil {
				continue
			}
			select {
			case out <- cfg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errs
}

func (s *Store) emitConfigLocked(cfg *core.PlatformConfig) {
	for _, w := range s.configWatchers {
		w.set(proto.Clone(cfg).(*core.PlatformConfig))
	}
}

func validateFunction(function *core.FunctionSpec) error {
	if function == nil {
		return status.Error(codes.InvalidArgument, "function is required")
	}
	if function.GetUserId() == 0 {
		return status.Error(codes.InvalidArgument, "function.user_id is required")
	}
	if function.GetRuntime() == nil {
		return status.Error(codes.InvalidArgument, "function.runtime is required")
	}
	if function.GetRuntime().GetImage() == "" {
		return status.Error(codes.InvalidArgument, "function.runtime.image is required")
	}
	protocol := function.GetRuntime().GetProtocol()
	if protocol != "http" && protocol != "grpc" {
		return status.Errorf(codes.InvalidArgument, "function.runtime.protocol must be http or grpc, got %q", protocol)
	}
	return nil
}

func cloneUser(in *core.UserSpec) *core.UserSpec {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*core.UserSpec)
}

func cloneFunction(in *core.FunctionSpec) *core.FunctionSpec {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*core.FunctionSpec)
}

func cloneEvent(in *core.FunctionEvent) *core.FunctionEvent {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*core.FunctionEvent)
}
