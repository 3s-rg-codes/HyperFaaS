package controlplane

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/controlplane/store"
	etcdstore "hyperfaas-ideal-arch/pkg/controlplane/store/etcd"
	"hyperfaas-ideal-arch/pkg/controlplane/store/memory"
	"hyperfaas-ideal-arch/pkg/core/utils"
)

// ControlPlaneServer hosts HTTP and gRPC metadata APIs.
type ControlPlaneServer struct {
	cfg    ControlPlaneConfig
	logger *slog.Logger
	store  store.Backend
}

type serverOption func(*ControlPlaneServer)

func WithStore(st store.Backend) serverOption {
	return func(s *ControlPlaneServer) { s.store = st }
}

func NewServer(cfg ControlPlaneConfig, logger *slog.Logger, opts ...serverOption) (*ControlPlaneServer, error) {
	if logger == nil {
		return nil, fmt.Errorf("controlplane: logger is required")
	}
	s := &ControlPlaneServer{cfg: cfg, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	if s.store == nil {
		st, err := OpenStore(cfg.Store)
		if err != nil {
			return nil, err
		}
		s.store = st
	}
	return s, nil
}

func OpenStore(cfg StoreConfig) (store.Backend, error) {
	switch cfg.Type {
	case "etcd":
		return etcdstore.New(cfg.Endpoints, cfg.Prefix, cfg.DialTimeout)
	default:
		return nil, fmt.Errorf("unsupported store type %q", cfg.Type)
	}
}

func NewMemoryStore() store.Backend {
	return memory.New()
}

func (s *ControlPlaneServer) Store() store.Backend {
	return s.store
}

// seedPlatformConfig writes the deployment default once. Components must always
// receive a configuration document, so a fresh deployment must not start with
// an empty config store.
func (s *ControlPlaneServer) seedPlatformConfig(ctx context.Context) error {
	if _, err := s.store.GetPlatformConfig(ctx); err == nil {
		return nil
	} else if status.Code(err) != codes.NotFound {
		return err
	}
	_, err := s.store.PutPlatformConfig(ctx, DefaultPlatformConfig(), 0)
	return err
}

func (s *ControlPlaneServer) Run(ctx context.Context) error {
	stopPprof := utils.StartPprof(ctx, s.logger, s.cfg.Server.PprofAddress)
	defer stopPprof()

	api := newGRPCAPI(s.store)
	httpHandler := newHTTPAPI(s.store)

	if err := s.seedPlatformConfig(ctx); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- utils.RunHTTPServer(ctx, s.logger, s.cfg.Server.HTTPAddress, httpHandler)
	}()
	go func() {
		errCh <- utils.RunGRPCServer(ctx, utils.GRPCServerOptions{
			Logger:      s.logger,
			ListenAddr:  s.cfg.Server.GRPCAddress,
			ServiceName: "controlplane",
			Register: func(grpcServer *grpc.Server) {
				RegisterFunctionServiceServer(grpcServer, api)
				RegisterUserServiceServer(grpcServer, api)
				RegisterConfigServiceServer(grpcServer, api)
			},
		})
	}()

	select {
	case <-ctx.Done():
		<-errCh
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		cancel()
		<-errCh
		if err != nil && ctx.Err() == nil {
			return err
		}
		return ctx.Err()
	}
}

func (s *ControlPlaneServer) Close() error {
	if s.store == nil {
		return nil
	}
	return s.store.Close()
}
