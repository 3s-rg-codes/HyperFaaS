package controlplane

import (
	"context"

	"hyperfaas-ideal-arch/pkg/core"
)

type UserStore interface {
	CreateUser(ctx context.Context, user *core.UserSpec) error
	UpdateUser(ctx context.Context, user *core.UserSpec) error
	DeleteUser(ctx context.Context, userID uint64) error
	GetUser(ctx context.Context, userID uint64) (*core.UserSpec, error)
	ListUsers(ctx context.Context) ([]*core.UserSpec, error)
}

type FunctionStore interface {
	CreateFunction(ctx context.Context, function *core.FunctionSpec) error
	UpdateFunction(ctx context.Context, function *core.FunctionSpec) error
	// Maybe we will need a DrainFunction later. What happens if a function is deleted as it's being called? Or we could fail fast.
	DeleteFunction(ctx context.Context, userID uint64, functionID uint64) error
	GetFunction(ctx context.Context, userID uint64, functionID uint64) (*core.FunctionSpec, error)
	ListFunctions(ctx context.Context, userID uint64) ([]*core.FunctionSpec, error)
	WatchFunctions(ctx context.Context) (<-chan *core.FunctionEvent, <-chan error)
}

// ConfigStore is the dynamic platform-configuration surface. It is separate
// from function metadata because it changes platform behavior (routing,
// placement) rather than what is deployed.
type ConfigStore interface {
	GetPlatformConfig(ctx context.Context) (*core.PlatformConfig, error)
	PutPlatformConfig(ctx context.Context, config *core.PlatformConfig, expectedVersion uint64) (*core.PlatformConfig, error)
	WatchPlatformConfig(ctx context.Context) (<-chan *core.PlatformConfig, <-chan error)
}

// PublicAPI is the user-facing control plane. Its implementation should validate requests and write desired state to the metadata store; leaves observe function changes separately.
type PublicAPI interface {
	UserStore
	FunctionStore
	ConfigStore
}
