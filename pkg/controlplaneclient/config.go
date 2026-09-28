package controlplaneclient

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/core"
)

// ConfigSubscriber keeps a component's view of the dynamic platform
// configuration in sync with the control plane.
//
// The document is immutable: every accepted version is a fresh
// *core.PlatformConfig, so readers can read Current on their own goroutines
// without a lock. Policy changes are delivered through onChange, which the
// caller uses to install the new policy. The version check makes reconnects and
// duplicate watch deliveries idempotent.
type ConfigSubscriber struct {
	address     string
	dialTimeout time.Duration
	backoff     time.Duration
	logger      *slog.Logger
	onChange    func(*core.PlatformConfig)
	current     atomic.Pointer[core.PlatformConfig]
}

// NewConfigSubscriber creates a subscriber. onChange is called for every
// accepted document, including the first one delivered by the watch; it runs on
// the subscriber goroutine and must be cheap and non-blocking.
func NewConfigSubscriber(
	address string,
	dialTimeout, backoff time.Duration,
	logger *slog.Logger,
	onChange func(*core.PlatformConfig),
) *ConfigSubscriber {
	return &ConfigSubscriber{
		address:     address,
		dialTimeout: dialTimeout,
		backoff:     backoff,
		logger:      logger,
		onChange:    onChange,
	}
}

// Current returns the latest accepted document, or nil before the first one
// has been applied. Callers must treat the returned value as read-only.
func (s *ConfigSubscriber) Current() *core.PlatformConfig {
	return s.current.Load()
}

// Run watches until ctx is cancelled, reconnecting with the configured backoff.
// It blocks and is meant to run in its own goroutine.
func (s *ConfigSubscriber) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.session(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("platform config watch ended", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.backoff):
			}
		}
	}
}

func (s *ConfigSubscriber) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, s.dialTimeout)
	conn, err := grpc.DialContext(dialCtx, s.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancel()
	if err != nil {
		return fmt.Errorf("dial control plane: %w", err)
	}
	defer conn.Close()

	client := controlplane.NewConfigServiceClient(conn)
	stream, err := client.WatchPlatformConfig(ctx, &controlplane.WatchPlatformConfigRequest{})
	if err != nil {
		return fmt.Errorf("watch platform config: %w", err)
	}
	for {
		cfg, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("receive platform config: %w", err)
		}
		s.apply(cfg)
	}
}

// apply installs a document if it is newer than the current one.
func (s *ConfigSubscriber) apply(cfg *core.PlatformConfig) {
	if cfg == nil {
		return
	}
	if current := s.current.Load(); current != nil && cfg.GetVersion() <= current.GetVersion() {
		return
	}
	s.current.Store(cfg)
	if s.onChange != nil {
		s.onChange(cfg)
	}
}
