package grpcproxy

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const metadataFunctionID = "x-hyperfaas-function-id"

// FunctionIDFromAuthority extracts the function ID from ingress routing metadata,
// falling back to the :authority pseudo-header for direct leaf-proxy calls.
func FunctionIDFromAuthority(_ context.Context, md metadata.MD) (uint64, error) {
	if values := md.Get(metadataFunctionID); len(values) > 0 {
		return parseFunctionID(values[0], metadataFunctionID)
	}

	authorityValues := md[":authority"]
	if len(authorityValues) == 0 {
		authorityValues = md["authority"]
	}
	if len(authorityValues) == 0 {
		return 0, status.Error(codes.InvalidArgument, "missing :authority header for function routing")
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
	return id, nil
}
