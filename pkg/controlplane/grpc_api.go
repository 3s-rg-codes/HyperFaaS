package controlplane

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"hyperfaas-ideal-arch/pkg/controlplane/store"
	"hyperfaas-ideal-arch/pkg/core"
)

type grpcAPI struct {
	UnimplementedFunctionServiceServer
	UnimplementedUserServiceServer
	UnimplementedConfigServiceServer
	store store.Backend
}

func newGRPCAPI(store store.Backend) *grpcAPI {
	return &grpcAPI{store: store}
}

func (a *grpcAPI) CreateFunction(ctx context.Context, req *CreateFunctionRequest) (*CreateFunctionResponse, error) {
	if err := a.store.CreateFunction(ctx, req.GetFunction()); err != nil {
		return nil, err
	}
	return &CreateFunctionResponse{}, nil
}

func (a *grpcAPI) UpdateFunction(ctx context.Context, req *UpdateFunctionRequest) (*UpdateFunctionResponse, error) {
	if err := a.store.UpdateFunction(ctx, req.GetFunction()); err != nil {
		return nil, err
	}
	return &UpdateFunctionResponse{}, nil
}

func (a *grpcAPI) DeleteFunction(ctx context.Context, req *DeleteFunctionRequest) (*DeleteFunctionResponse, error) {
	if err := a.store.DeleteFunction(ctx, req.GetUserId(), req.GetFunctionId()); err != nil {
		return nil, err
	}
	return &DeleteFunctionResponse{}, nil
}

func (a *grpcAPI) GetFunction(ctx context.Context, req *GetFunctionRequest) (*GetFunctionResponse, error) {
	fn, err := a.store.GetFunction(ctx, req.GetUserId(), req.GetFunctionId())
	if err != nil {
		return nil, err
	}
	return &GetFunctionResponse{Function: fn}, nil
}

func (a *grpcAPI) ListFunctions(ctx context.Context, req *ListFunctionsRequest) (*ListFunctionsResponse, error) {
	functions, err := a.store.ListFunctions(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &ListFunctionsResponse{Functions: functions}, nil
}

func (a *grpcAPI) WatchFunctions(req *WatchFunctionsRequest, stream grpc.ServerStreamingServer[core.FunctionEvent]) error {
	events, errs := a.store.WatchFunctions(stream.Context())
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-errs:
			if err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

func (a *grpcAPI) CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error) {
	if err := a.store.CreateUser(ctx, req.GetUser()); err != nil {
		return nil, err
	}
	return &CreateUserResponse{}, nil
}

func (a *grpcAPI) UpdateUser(ctx context.Context, req *UpdateUserRequest) (*UpdateUserResponse, error) {
	if err := a.store.UpdateUser(ctx, req.GetUser()); err != nil {
		return nil, err
	}
	return &UpdateUserResponse{}, nil
}

func (a *grpcAPI) DeleteUser(ctx context.Context, req *DeleteUserRequest) (*DeleteUserResponse, error) {
	if err := a.store.DeleteUser(ctx, req.GetUserId()); err != nil {
		return nil, err
	}
	return &DeleteUserResponse{}, nil
}

func (a *grpcAPI) GetUser(ctx context.Context, req *GetUserRequest) (*GetUserResponse, error) {
	user, err := a.store.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &GetUserResponse{User: user}, nil
}

func (a *grpcAPI) ListUsers(ctx context.Context, _ *ListUsersRequest) (*ListUsersResponse, error) {
	users, err := a.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	return &ListUsersResponse{Users: users}, nil
}

func (a *grpcAPI) GetPlatformConfig(ctx context.Context, _ *GetPlatformConfigRequest) (*GetPlatformConfigResponse, error) {
	cfg, err := a.store.GetPlatformConfig(ctx)
	if err != nil {
		return nil, grpcStatus(err)
	}
	return &GetPlatformConfigResponse{Config: cfg}, nil
}

func (a *grpcAPI) PutPlatformConfig(ctx context.Context, req *PutPlatformConfigRequest) (*PutPlatformConfigResponse, error) {
	if err := core.ValidatePlatformConfig(req.GetConfig()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	stored, err := a.store.PutPlatformConfig(ctx, req.GetConfig(), req.GetExpectedVersion())
	if err != nil {
		return nil, grpcStatus(err)
	}
	return &PutPlatformConfigResponse{Config: stored}, nil
}

func (a *grpcAPI) WatchPlatformConfig(_ *WatchPlatformConfigRequest, stream grpc.ServerStreamingServer[core.PlatformConfig]) error {
	configs, errs := a.store.WatchPlatformConfig(stream.Context())
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-errs:
			if err != nil {
				return err
			}
		case cfg, ok := <-configs:
			if !ok {
				return nil
			}
			if err := stream.Send(cfg); err != nil {
				return err
			}
		}
	}
}

func grpcStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "%v", err)
}
