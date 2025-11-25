package metadata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

var (
	ErrFunctionNotFound      = errors.New("metadata: function not found")
	ErrFunctionIDIsEmpty     = errors.New("metadata: function id is empty")
	ErrFunctionAlreadyExists = errors.New("metadata: function already exists")
	ErrReqIsNil              = errors.New("metadata: request is nil")
)

type Client interface {
	PutFunction(ctx context.Context, req *common.CreateFunctionRequest) (string, error)
	PutFunctionWithID(ctx context.Context, id string, req *common.CreateFunctionRequest) error
	DeleteFunction(ctx context.Context, id string) error
	GetFunction(ctx context.Context, id string) (*FunctionMetadata, error)
	ListFunctions(ctx context.Context) (*ListResult, error)
	WatchFunctions(ctx context.Context, revision int64) (<-chan Event, <-chan error)
	Close() error
}

// EtcdClient wraps etcd to manage HyperFaaS function metadata.
type EtcdClient struct {
	cli    *clientv3.Client
	prefix string
	logger *slog.Logger
}

// NewClient connects to etcd using the provided endpoints and options.
func NewClient(endpoints []string, opts Options, logger *slog.Logger) (*EtcdClient, error) {
	if len(endpoints) == 0 {
		return nil, errors.New("metadata: at least one etcd endpoint is required")
	}

	prefix := opts.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}

	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}

	cfg := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	}

	cli, err := clientv3.New(cfg)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &EtcdClient{cli: cli, prefix: normalizePrefix(prefix), logger: logger}, nil
}

// Close releases the etcd client.
func (c *EtcdClient) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// PutFunction stores new function metadata and generates a function ID.
func (c *EtcdClient) PutFunction(ctx context.Context, req *common.CreateFunctionRequest) (string, error) {
	if req == nil {
		return "", ErrReqIsNil
	}
	id := uuid.NewString()
	if err := c.PutFunctionWithID(ctx, id, req); err != nil {
		return "", err
	}
	return id, nil
}

// PutFunctionWithID stores or updates metadata for a given function ID.
func (c *EtcdClient) PutFunctionWithID(ctx context.Context, id string, req *common.CreateFunctionRequest) error {
	if id == "" {
		return ErrFunctionIDIsEmpty
	}
	if req == nil {
		return ErrReqIsNil
	}

	payload, err := protojson.Marshal(req)
	if err != nil {
		return err
	}

	_, err = c.cli.Put(ctx, c.key(id), string(payload))
	return err
}

// DeleteFunction removes metadata for the given function ID.
func (c *EtcdClient) DeleteFunction(ctx context.Context, id string) error {
	if id == "" {
		return ErrFunctionIDIsEmpty
	}
	resp, err := c.cli.Delete(ctx, c.key(id))
	if err != nil {
		return err
	}
	if resp.Deleted == 0 {
		return ErrFunctionNotFound
	}
	return nil
}

// GetFunction returns the metadata for the given function ID.
func (c *EtcdClient) GetFunction(ctx context.Context, id string) (*FunctionMetadata, error) {
	if id == "" {
		return nil, ErrFunctionIDIsEmpty
	}

	resp, err := c.cli.Get(ctx, c.key(id))
	if err != nil {
		return nil, err
	}

	if len(resp.Kvs) == 0 {
		return nil, ErrFunctionNotFound
	}

	meta, err := decodeFunctionMetadata(resp.Kvs[0].Value, id)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// ListFunctions loads all function metadata stored under the configured prefix.
func (c *EtcdClient) ListFunctions(ctx context.Context) (*ListResult, error) {
	resp, err := c.cli.Get(ctx, c.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	functions := make([]*FunctionMetadata, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		id := c.idFromKey(string(kv.Key))
		meta, err := decodeFunctionMetadata(kv.Value, id)
		if err != nil {
			c.logger.Error("failed to decode function metadata", "id", id, "error", err)
			continue
		}
		functions = append(functions, meta)
	}

	return &ListResult{Functions: functions, Revision: resp.Header.Revision}, nil
}

// WatchFunctions streams metadata changes starting after the provided revision.
func (c *EtcdClient) WatchFunctions(ctx context.Context, revision int64) (<-chan Event, <-chan error) {
	events := make(chan Event, 64)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		opts := []clientv3.OpOption{clientv3.WithPrefix()}
		if revision > 0 {
			opts = append(opts, clientv3.WithRev(revision+1))
		}

		watch := c.cli.Watch(ctx, c.prefix, opts...)
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-watch:
				if !ok {
					return
				}
				if err := resp.Err(); err != nil {
					select {
					case errs <- err:
					default:
					}
					continue
				}
				for _, ev := range resp.Events {
					id := c.idFromKey(string(ev.Kv.Key))
					switch ev.Type {
					case mvccpb.PUT:
						meta, err := decodeFunctionMetadata(ev.Kv.Value, id)
						if err != nil {
							c.logger.Error("failed to decode function metadata", "id", id, "error", err)
							continue
						}
						select {
						case events <- Event{Type: EventTypePut, FunctionID: id, Function: meta}:
						case <-ctx.Done():
							return
						}
					case mvccpb.DELETE:
						select {
						case events <- Event{Type: EventTypeDelete, FunctionID: id}:
						case <-ctx.Done():
							return
						}
					default:
						c.logger.Warn("received unknown event type", "type", ev.Type, "id", id)
					}
				}
			}
		}
	}()

	return events, errs
}

func (c *EtcdClient) key(id string) string {
	return c.prefix + "/" + id
}

func (c *EtcdClient) idFromKey(key string) string {
	return strings.TrimPrefix(strings.TrimPrefix(key, c.prefix), "/")
}

func decodeFunctionMetadata(raw []byte, id string) (*FunctionMetadata, error) {
	var req common.CreateFunctionRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return &FunctionMetadata{
		ID:     id,
		Image:  req.GetImage(),
		Config: req.GetConfig(),
	}, nil
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return DefaultPrefix
	}
	return prefix
}
