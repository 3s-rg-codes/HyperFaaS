package controlplane

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"hyperfaas-ideal-arch/pkg/controlplane/store"
	"hyperfaas-ideal-arch/pkg/core"
)

type httpAPI struct {
	store store.Backend
}

func newHTTPAPI(store store.Backend) http.Handler {
	api := &httpAPI{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/users", api.listUsers)
	mux.HandleFunc("POST /v1/users", api.createUser)
	mux.HandleFunc("GET /v1/users/{user_id}", api.getUser)
	mux.HandleFunc("PUT /v1/users/{user_id}", api.updateUser)
	mux.HandleFunc("DELETE /v1/users/{user_id}", api.deleteUser)
	mux.HandleFunc("GET /v1/users/{user_id}/functions", api.listFunctions)
	mux.HandleFunc("POST /v1/users/{user_id}/functions", api.createFunction)
	mux.HandleFunc("GET /v1/users/{user_id}/functions/{function_id}", api.getFunction)
	mux.HandleFunc("PUT /v1/users/{user_id}/functions/{function_id}", api.updateFunction)
	mux.HandleFunc("DELETE /v1/users/{user_id}/functions/{function_id}", api.deleteFunction)
	mux.HandleFunc("GET /v1/platform/config", api.getPlatformConfig)
	mux.HandleFunc("PUT /v1/platform/config", api.putPlatformConfig)
	return mux
}

func (a *httpAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *httpAPI) createUser(w http.ResponseWriter, r *http.Request) {
	user, err := decodeUser(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if err := a.store.CreateUser(r.Context(), user); err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (a *httpAPI) getUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	user, err := a.store.GetUser(r.Context(), userID)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *httpAPI) updateUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	user, err := decodeUser(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	user.UserId = userID
	if err := a.store.UpdateUser(r.Context(), user); err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *httpAPI) deleteUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if err := a.store.DeleteUser(r.Context(), userID); err != nil {
		writeHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *httpAPI) listFunctions(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	functions, err := a.store.ListFunctions(r.Context(), userID)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"functions": functions})
}

func (a *httpAPI) createFunction(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	function, err := decodeFunction(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	function.UserId = userID
	if err := a.store.CreateFunction(r.Context(), function); err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, function)
}

func (a *httpAPI) getFunction(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	functionID, err := parsePathUint(r, "function_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	function, err := a.store.GetFunction(r.Context(), userID, functionID)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, function)
}

func (a *httpAPI) updateFunction(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	functionID, err := parsePathUint(r, "function_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	function, err := decodeFunction(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	function.UserId = userID
	function.FunctionId = functionID
	if err := a.store.UpdateFunction(r.Context(), function); err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, function)
}

func (a *httpAPI) deleteFunction(w http.ResponseWriter, r *http.Request) {
	userID, err := parsePathUint(r, "user_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	functionID, err := parsePathUint(r, "function_id")
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if err := a.store.DeleteFunction(r.Context(), userID, functionID); err != nil {
		writeHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *httpAPI) getPlatformConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.store.GetPlatformConfig(r.Context())
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (a *httpAPI) putPlatformConfig(w http.ResponseWriter, r *http.Request) {
	req, err := decodePutPlatformConfig(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if err := core.ValidatePlatformConfig(req.GetConfig()); err != nil {
		writeHTTPError(w, status.Errorf(codes.InvalidArgument, "%v", err))
		return
	}
	stored, err := a.store.PutPlatformConfig(r.Context(), req.GetConfig(), req.GetExpectedVersion())
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func decodeUser(r io.Reader) (*core.UserSpec, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	user := &core.UserSpec{}
	if err := protojson.Unmarshal(data, user); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user json: %v", err)
	}
	return user, nil
}

func decodeFunction(r io.Reader) (*core.FunctionSpec, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	function := &core.FunctionSpec{}
	if err := protojson.Unmarshal(data, function); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid function json: %v", err)
	}
	return function, nil
}

func decodePutPlatformConfig(r io.Reader) (*PutPlatformConfigRequest, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	req := &PutPlatformConfigRequest{}
	if err := protojson.Unmarshal(data, req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid platform config json: %v", err)
	}
	return req, nil
}

func parsePathUint(r *http.Request, name string) (uint64, error) {
	value := strings.TrimSpace(r.PathValue(name))
	if value == "" {
		return 0, status.Errorf(codes.InvalidArgument, "%s is required", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "%s must be a positive integer", name)
	}
	return parsed, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	switch v := payload.(type) {
	case *core.UserSpec:
		data, err := protojson.Marshal(v)
		if err != nil {
			writeHTTPError(w, err)
			return
		}
		_, _ = w.Write(data)
	case *core.FunctionSpec:
		data, err := protojson.Marshal(v)
		if err != nil {
			writeHTTPError(w, err)
			return
		}
		_, _ = w.Write(data)
	case *core.PlatformConfig:
		data, err := protojson.Marshal(v)
		if err != nil {
			writeHTTPError(w, err)
			return
		}
		_, _ = w.Write(data)
	default:
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeHTTPError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Error(w, st.Message(), grpcCodeToHTTP(st.Code()))
}

func grpcCodeToHTTP(code codes.Code) int {
	switch code {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.Unimplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}
