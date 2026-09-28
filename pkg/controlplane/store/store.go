package store

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

// Backend is a metadata store used by the control plane and simulation stack.
type Backend interface {
	CreateUser(ctx context.Context, user *core.UserSpec) error
	UpdateUser(ctx context.Context, user *core.UserSpec) error
	DeleteUser(ctx context.Context, userID uint64) error
	GetUser(ctx context.Context, userID uint64) (*core.UserSpec, error)
	ListUsers(ctx context.Context) ([]*core.UserSpec, error)

	CreateFunction(ctx context.Context, function *core.FunctionSpec) error
	UpdateFunction(ctx context.Context, function *core.FunctionSpec) error
	DeleteFunction(ctx context.Context, userID, functionID uint64) error
	GetFunction(ctx context.Context, userID, functionID uint64) (*core.FunctionSpec, error)
	ListFunctions(ctx context.Context, userID uint64) ([]*core.FunctionSpec, error)
	WatchFunctions(ctx context.Context) (<-chan *core.FunctionEvent, <-chan error)

	// GetPlatformConfig returns the current dynamic platform configuration. It
	// returns codes.NotFound when no configuration has been written yet, which
	// lets a component keep its static YAML defaults until an operator
	// publishes the first document.
	GetPlatformConfig(ctx context.Context) (*core.PlatformConfig, error)
	// PutPlatformConfig stores a new dynamic platform configuration under a
	// single well-known key and assigns it a monotonically increasing version.
	// When expectedVersion is non-zero the write is rejected with
	// codes.Aborted unless the stored document already has that version, which
	// prevents concurrent writers from silently replacing each other. The
	// stored document is returned with its assigned version.
	PutPlatformConfig(ctx context.Context, config *core.PlatformConfig, expectedVersion uint64) (*core.PlatformConfig, error)
	// WatchPlatformConfig streams the current configuration (if one exists)
	// followed by every later version. The current value is emitted first, so a
	// subscriber never needs a separate Get/watch-revision handshake.
	WatchPlatformConfig(ctx context.Context) (<-chan *core.PlatformConfig, <-chan error)

	Close() error
}
