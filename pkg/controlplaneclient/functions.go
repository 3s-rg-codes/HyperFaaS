// Package controlplaneclient contains the control-plane subscriptions a
// component uses at runtime.
//
// There are two independent streams. FunctionSubscriber delivers deployed
// function metadata (what exists). ConfigSubscriber delivers the dynamic
// platform configuration (how the platform behaves: routing, placement).
// Both live here so there is one home for "how a component talks
// to the control plane".
package controlplaneclient

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/core"
	leafpkg "hyperfaas-ideal-arch/pkg/leaf"
)

// FunctionSubscriber implements leaf.FunctionSubscriber against the control
// plane gRPC API. It replays the current function set, then follows the event
// stream, deduplicating creations so a reconnect does not deliver a duplicate
// CREATED for a function that is already known.
type FunctionSubscriber struct {
	address     string
	dialTimeout time.Duration
	backoff     time.Duration
	logger      *slog.Logger
}

func NewFunctionSubscriber(address string, dialTimeout, backoff time.Duration, logger *slog.Logger) *FunctionSubscriber {
	return &FunctionSubscriber{
		address:     address,
		dialTimeout: dialTimeout,
		backoff:     backoff,
		logger:      logger,
	}
}

func (s *FunctionSubscriber) SubscribeFunctions(ctx context.Context) (<-chan *core.FunctionEvent, <-chan error) {
	events := make(chan *core.FunctionEvent, 32)
	errs := make(chan error, 1)
	go s.run(ctx, events, errs)
	return events, errs
}

func (s *FunctionSubscriber) run(ctx context.Context, events chan<- *core.FunctionEvent, errs chan<- error) {
	defer close(events)
	defer close(errs)

	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.session(ctx, events); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("control plane subscription ended", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.backoff):
			}
		}
	}
}

func (s *FunctionSubscriber) session(ctx context.Context, events chan<- *core.FunctionEvent) error {
	dialCtx, cancel := context.WithTimeout(ctx, s.dialTimeout)
	conn, err := grpc.DialContext(dialCtx, s.address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	cancel()
	if err != nil {
		return fmt.Errorf("dial control plane: %w", err)
	}
	defer conn.Close()

	fnClient := controlplane.NewFunctionServiceClient(conn)
	userClient := controlplane.NewUserServiceClient(conn)

	seen := make(map[uint64]struct{})
	users, err := userClient.ListUsers(ctx, &controlplane.ListUsersRequest{})
	if err != nil {
		return err
	}
	now := timestamppb.Now()
	for _, user := range users.GetUsers() {
		resp, err := fnClient.ListFunctions(ctx, &controlplane.ListFunctionsRequest{UserId: user.GetUserId()})
		if err != nil {
			return err
		}
		for _, fn := range resp.GetFunctions() {
			seen[fn.GetFunctionId()] = struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case events <- &core.FunctionEvent{
				Type:       core.FunctionEventType_FUNCTION_EVENT_TYPE_CREATED,
				Function:   fn,
				ObservedAt: now,
			}:
			}
		}
	}

	stream, err := fnClient.WatchFunctions(ctx, &controlplane.WatchFunctionsRequest{})
	if err != nil {
		return err
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		functionID := ev.GetFunction().GetFunctionId()
		if ev.GetType() == core.FunctionEventType_FUNCTION_EVENT_TYPE_CREATED {
			if _, dup := seen[functionID]; dup {
				continue
			}
			seen[functionID] = struct{}{}
		}
		if ev.GetType() == core.FunctionEventType_FUNCTION_EVENT_TYPE_DELETED {
			delete(seen, functionID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- ev:
		}
	}
}

var _ leafpkg.FunctionSubscriber = (*FunctionSubscriber)(nil)
