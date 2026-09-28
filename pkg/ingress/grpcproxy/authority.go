package grpcproxy

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const MetadataFunctionID = "x-hyperfaas-function-id"

// FunctionIDFromMetadata extracts the function ID from routing metadata.
// Prefers x-hyperfaas-function-id; falls back to the host part of :authority.
func FunctionIDFromMetadata(_ context.Context, md metadata.MD) (uint64, error) {
	if values := md.Get(MetadataFunctionID); len(values) > 0 {
		id, err := parseFunctionID(values[0], MetadataFunctionID)
		if err != nil {
			return 0, err
		}
		return id, nil
	}

	authorityValues := md[":authority"]
	if len(authorityValues) == 0 {
		authorityValues = md["authority"]
	}
	if len(authorityValues) == 0 {
		return 0, status.Error(codes.InvalidArgument, "missing x-hyperfaas-function-id and :authority for function routing")
	}
	host := authorityValues[0]
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return parseFunctionID(host, ":authority")
}

func parseFunctionID(value, source string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, status.Errorf(codes.InvalidArgument, "empty function id in %s", source)
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid function id %q in %s", value, source)
	}
	if id == 0 {
		return 0, status.Errorf(codes.InvalidArgument, "invalid function id %q in %s", value, source)
	}
	return id, nil
}
