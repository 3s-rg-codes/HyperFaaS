package etcd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/core"
)

type Store struct {
	client *clientv3.Client
	prefix string

	mu             sync.Mutex
	nextUserID     uint64
	nextFunctionID uint64
}

func New(endpoints []string, prefix string, dialTimeout time.Duration) (*Store, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("etcd endpoints are required")
	}
	if prefix == "" {
		return nil, fmt.Errorf("etcd prefix is required")
	}
	if dialTimeout <= 0 {
		return nil, fmt.Errorf("etcd dial_timeout is required")
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create etcd client: %w", err)
	}

	s := &Store{
		client:         client,
		prefix:         strings.TrimSuffix(prefix, "/"),
		nextUserID:     1,
		nextFunctionID: 1,
	}
	if err := s.bootstrapCounters(context.Background()); err != nil {
		_ = client.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}

func (s *Store) bootstrapCounters(ctx context.Context) error {
	resp, err := s.client.Get(ctx, s.prefix+"/users/", clientv3.WithPrefix())
	if err != nil {
		return err
	}
	for _, kv := range resp.Kvs {
		id, err := parseTrailingID(string(kv.Key))
		if err == nil && id >= s.nextUserID {
			s.nextUserID = id + 1
		}
	}
	resp, err = s.client.Get(ctx, s.prefix+"/functions/", clientv3.WithPrefix())
	if err != nil {
		return err
	}
	for _, kv := range resp.Kvs {
		_, functionID, err := parseFunctionKey(string(kv.Key), s.prefix)
		if err == nil && functionID >= s.nextFunctionID {
			s.nextFunctionID = functionID + 1
		}
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, user *core.UserSpec) error {
	if user == nil || user.GetName() == "" {
		return status.Error(codes.InvalidArgument, "user.name is required")
	}

	s.mu.Lock()
	id := user.GetUserId()
	if id == 0 {
		id = s.nextUserID
		s.nextUserID++
		user.UserId = id
	}
	s.mu.Unlock()

	return s.putIfAbsent(ctx, s.userKey(id), user)
}

func (s *Store) UpdateUser(ctx context.Context, user *core.UserSpec) error {
	if user == nil || user.GetUserId() == 0 || user.GetName() == "" {
		return status.Error(codes.InvalidArgument, "user.user_id and user.name are required")
	}
	return s.putIfExists(ctx, s.userKey(user.GetUserId()), user)
}

func (s *Store) DeleteUser(ctx context.Context, userID uint64) error {
	if userID == 0 {
		return status.Error(codes.InvalidArgument, "user_id is required")
	}

	funcResp, err := s.client.Get(ctx, s.functionPrefix(userID), clientv3.WithPrefix())
	if err != nil {
		return err
	}

	ops := []clientv3.Op{clientv3.OpDelete(s.userKey(userID))}
	for _, kv := range funcResp.Kvs {
		ops = append(ops, clientv3.OpDelete(string(kv.Key)))
	}

	txn := s.client.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(s.userKey(userID)), ">", 0)).Then(ops...)
	resp, err := txn.Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return status.Errorf(codes.NotFound, "user %d not found", userID)
	}
	return nil
}

func (s *Store) GetUser(ctx context.Context, userID uint64) (*core.UserSpec, error) {
	resp, err := s.client.Get(ctx, s.userKey(userID))
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, status.Errorf(codes.NotFound, "user %d not found", userID)
	}
	return decodeUser(resp.Kvs[0].Value)
}

func (s *Store) ListUsers(ctx context.Context) ([]*core.UserSpec, error) {
	resp, err := s.client.Get(ctx, s.prefix+"/users/", clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	out := make([]*core.UserSpec, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		user, err := decodeUser(kv.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetUserId() < out[j].GetUserId() })
	return out, nil
}

func (s *Store) CreateFunction(ctx context.Context, function *core.FunctionSpec) error {
	if err := validateFunction(function); err != nil {
		return err
	}
	if _, err := s.GetUser(ctx, function.GetUserId()); err != nil {
		return err
	}

	s.mu.Lock()
	functionID := function.GetFunctionId()
	if functionID == 0 {
		functionID = s.nextFunctionID
		s.nextFunctionID++
		function.FunctionId = functionID
	}
	s.mu.Unlock()

	return s.putIfAbsent(ctx, s.functionKey(function.GetUserId(), functionID), function)
}

func (s *Store) UpdateFunction(ctx context.Context, function *core.FunctionSpec) error {
	if err := validateFunction(function); err != nil {
		return err
	}
	if function.GetFunctionId() == 0 {
		return status.Error(codes.InvalidArgument, "function.function_id is required")
	}
	return s.putIfExists(ctx, s.functionKey(function.GetUserId(), function.GetFunctionId()), function)
}

func (s *Store) DeleteFunction(ctx context.Context, userID, functionID uint64) error {
	if userID == 0 || functionID == 0 {
		return status.Error(codes.InvalidArgument, "user_id and function_id are required")
	}
	key := s.functionKey(userID, functionID)
	txn := s.client.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(key), ">", 0)).Then(clientv3.OpDelete(key))
	resp, err := txn.Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return status.Errorf(codes.NotFound, "function %d/%d not found", userID, functionID)
	}
	return nil
}

func (s *Store) GetFunction(ctx context.Context, userID, functionID uint64) (*core.FunctionSpec, error) {
	resp, err := s.client.Get(ctx, s.functionKey(userID, functionID))
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, status.Errorf(codes.NotFound, "function %d/%d not found", userID, functionID)
	}
	return decodeFunction(resp.Kvs[0].Value)
}

func (s *Store) ListFunctions(ctx context.Context, userID uint64) ([]*core.FunctionSpec, error) {
	resp, err := s.client.Get(ctx, s.functionPrefix(userID), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	out := make([]*core.FunctionSpec, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		fn, err := decodeFunction(kv.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, fn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetFunctionId() < out[j].GetFunctionId() })
	return out, nil
}

func (s *Store) WatchFunctions(ctx context.Context) (<-chan *core.FunctionEvent, <-chan error) {
	events := make(chan *core.FunctionEvent, 16)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		watchCh := s.client.Watch(ctx, s.prefix+"/functions/", clientv3.WithPrefix(), clientv3.WithPrevKV())
		for {
			select {
			case <-ctx.Done():
				return
			case watchResp, ok := <-watchCh:
				if !ok {
					return
				}
				if err := watchResp.Err(); err != nil {
					errs <- err
					return
				}
				for _, ev := range watchResp.Events {
					userID, functionID, err := parseFunctionKey(string(ev.Kv.Key), s.prefix)
					if err != nil {
						continue
					}

					var eventType core.FunctionEventType
					var fn *core.FunctionSpec
					switch ev.Type {
					case clientv3.EventTypePut:
						fn, err = decodeFunction(ev.Kv.Value)
						if err != nil {
							errs <- err
							return
						}
						if ev.Kv.CreateRevision == ev.Kv.ModRevision {
							eventType = core.FunctionEventType_FUNCTION_EVENT_TYPE_CREATED
						} else {
							eventType = core.FunctionEventType_FUNCTION_EVENT_TYPE_UPDATED
						}
					case clientv3.EventTypeDelete:
						eventType = core.FunctionEventType_FUNCTION_EVENT_TYPE_DELETED
						fn = &core.FunctionSpec{UserId: userID, FunctionId: functionID}
					default:
						continue
					}

					select {
					case events <- &core.FunctionEvent{
						Type:       eventType,
						Function:   fn,
						ObservedAt: timestamppb.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return events, errs
}

// platformConfigKey is the single etcd key that holds the deployment's dynamic
// platform configuration. There is exactly one document per deployment.
func (s *Store) platformConfigKey() string {
	return s.prefix + "/config/platform"
}

// GetPlatformConfig returns the stored document with its version derived from
// the etcd modification revision, so the version is monotonic without a
// separate counter that could drift across control-plane replicas.
func (s *Store) GetPlatformConfig(ctx context.Context) (*core.PlatformConfig, error) {
	resp, err := s.client.Get(ctx, s.platformConfigKey())
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, status.Error(codes.NotFound, "platform config not set")
	}
	cfg, err := decodePlatformConfig(resp.Kvs[0].Value)
	if err != nil {
		return nil, err
	}
	cfg.Version = uint64(resp.Kvs[0].ModRevision)
	return cfg, nil
}

// PutPlatformConfig overwrites the single config key. When expectedVersion is
// non-zero it must equal the stored document's modification revision, which
// makes the write a compare-and-swap so concurrent writers cannot silently
// replace each other. The document's version is the revision returned by etcd.
func (s *Store) PutPlatformConfig(ctx context.Context, cfg *core.PlatformConfig, expectedVersion uint64) (*core.PlatformConfig, error) {
	if cfg == nil {
		return nil, status.Error(codes.InvalidArgument, "platform config is required")
	}
	data, err := protojson.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	key := s.platformConfigKey()
	if expectedVersion == 0 {
		if _, err := s.client.Put(ctx, key, string(data)); err != nil {
			return nil, err
		}
	} else {
		resp, err := s.client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(key), "=", int64(expectedVersion))).
			Then(clientv3.OpPut(key, string(data))).
			Commit()
		if err != nil {
			return nil, err
		}
		if !resp.Succeeded {
			return nil, status.Errorf(codes.Aborted, "platform config version changed: expected %d", expectedVersion)
		}
	}
	// Read back the stored revision so the returned version always matches what
	// watchers will observe.
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, status.Error(codes.Internal, "platform config missing after write")
	}
	stored := proto.Clone(cfg).(*core.PlatformConfig)
	stored.Version = uint64(resp.Kvs[0].ModRevision)
	return stored, nil
}

// WatchPlatformConfig emits the current document (if any) and then every later
// revision. The initial Get and the watch are tied together through the Get
// header revision so no update can slip between them.
func (s *Store) WatchPlatformConfig(ctx context.Context) (<-chan *core.PlatformConfig, <-chan error) {
	events := make(chan *core.PlatformConfig, 8)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		key := s.platformConfigKey()
		resp, err := s.client.Get(ctx, key)
		if err != nil {
			errs <- err
			return
		}
		startRev := resp.Header.Revision + 1
		if len(resp.Kvs) > 0 {
			cfg, err := decodePlatformConfig(resp.Kvs[0].Value)
			if err != nil {
				errs <- err
				return
			}
			cfg.Version = uint64(resp.Kvs[0].ModRevision)
			select {
			case events <- cfg:
			case <-ctx.Done():
				return
			}
		}

		watchCh := s.client.Watch(ctx, key, clientv3.WithRev(startRev))
		for {
			select {
			case <-ctx.Done():
				return
			case watchResp, ok := <-watchCh:
				if !ok {
					return
				}
				if err := watchResp.Err(); err != nil {
					errs <- err
					return
				}
				for _, ev := range watchResp.Events {
					// A delete means the document was removed; components keep
					// their last applied configuration rather than reverting.
					if ev.Type == clientv3.EventTypeDelete {
						continue
					}
					cfg, err := decodePlatformConfig(ev.Kv.Value)
					if err != nil {
						errs <- err
						return
					}
					cfg.Version = uint64(ev.Kv.ModRevision)
					select {
					case events <- cfg:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return events, errs
}

func (s *Store) putIfAbsent(ctx context.Context, key string, msg any) error {
	data, err := marshal(msg)
	if err != nil {
		return err
	}
	txn := s.client.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).Then(clientv3.OpPut(key, string(data)))
	resp, err := txn.Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return status.Error(codes.AlreadyExists, "resource already exists")
	}
	return nil
}

func (s *Store) putIfExists(ctx context.Context, key string, msg any) error {
	data, err := marshal(msg)
	if err != nil {
		return err
	}
	txn := s.client.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(key), ">", 0)).Then(clientv3.OpPut(key, string(data)))
	resp, err := txn.Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return status.Error(codes.NotFound, "resource not found")
	}
	return nil
}

func marshal(msg any) ([]byte, error) {
	switch v := msg.(type) {
	case *core.UserSpec:
		return protojson.Marshal(v)
	case *core.FunctionSpec:
		return protojson.Marshal(v)
	default:
		return nil, fmt.Errorf("unsupported message type %T", msg)
	}
}

func decodeUser(data []byte) (*core.UserSpec, error) {
	out := &core.UserSpec{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeFunction(data []byte) (*core.FunctionSpec, error) {
	out := &core.FunctionSpec{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodePlatformConfig(data []byte) (*core.PlatformConfig, error) {
	out := &core.PlatformConfig{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateFunction(function *core.FunctionSpec) error {
	if function == nil {
		return status.Error(codes.InvalidArgument, "function is required")
	}
	if function.GetUserId() == 0 {
		return status.Error(codes.InvalidArgument, "function.user_id is required")
	}
	if function.GetRuntime() == nil || function.GetRuntime().GetImage() == "" {
		return status.Error(codes.InvalidArgument, "function.runtime.image is required")
	}
	protocol := function.GetRuntime().GetProtocol()
	if protocol != "http" && protocol != "grpc" {
		return status.Errorf(codes.InvalidArgument, "function.runtime.protocol must be http or grpc, got %q", protocol)
	}
	return nil
}

func (s *Store) userKey(userID uint64) string {
	return fmt.Sprintf("%s/users/%d", s.prefix, userID)
}

func (s *Store) functionKey(userID, functionID uint64) string {
	return fmt.Sprintf("%s/functions/%d/%d", s.prefix, userID, functionID)
}

func (s *Store) functionPrefix(userID uint64) string {
	return fmt.Sprintf("%s/functions/%d/", s.prefix, userID)
}

func parseFunctionKey(key, prefix string) (uint64, uint64, error) {
	trimmed := strings.TrimPrefix(key, prefix+"/functions/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid function key %q", key)
	}
	userID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	functionID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return userID, functionID, nil
}

func parseTrailingID(key string) (uint64, error) {
	parts := strings.Split(key, "/")
	return strconv.ParseUint(parts[len(parts)-1], 10, 64)
}
