package shared

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"google.golang.org/protobuf/encoding/protojson"

	"hyperfaas-ideal-arch/pkg/controlplane"
	"hyperfaas-ideal-arch/pkg/core"
)

type ControlPlaneClient struct {
	base   string
	client *http.Client
	log    *slog.Logger
}

func NewControlPlaneClient(cfg Config, log *slog.Logger) *ControlPlaneClient {
	return &ControlPlaneClient{
		base:   "http://" + cfg.ControlPlaneHTTP,
		client: http.DefaultClient,
		log:    log,
	}
}

func (c *ControlPlaneClient) CreateUser(ctx context.Context, name string) (*core.UserSpec, error) {
	body, err := protojson.Marshal(&core.UserSpec{Name: name})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/users", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doUser(req)
}

func (c *ControlPlaneClient) UpdateUser(ctx context.Context, user *core.UserSpec) (*core.UserSpec, error) {
	body, err := protojson.Marshal(user)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/v1/users/%d", c.base, user.GetUserId())
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doUser(req)
}

func (c *ControlPlaneClient) GetUser(ctx context.Context, userID uint64) (*core.UserSpec, error) {
	url := fmt.Sprintf("%s/v1/users/%d", c.base, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.doUser(req)
}

func (c *ControlPlaneClient) DeleteUser(ctx context.Context, userID uint64) error {
	url := fmt.Sprintf("%s/v1/users/%d", c.base, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	_, err = c.do(req, http.StatusNoContent)
	return err
}

func (c *ControlPlaneClient) CreateFunction(ctx context.Context, userID uint64, fn *core.FunctionSpec) (*core.FunctionSpec, error) {
	fn.UserId = userID
	body, err := protojson.Marshal(fn)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/v1/users/%d/functions", c.base, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doFunction(req)
}

func (c *ControlPlaneClient) UpdateFunction(ctx context.Context, userID uint64, fn *core.FunctionSpec) (*core.FunctionSpec, error) {
	fn.UserId = userID
	body, err := protojson.Marshal(fn)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/v1/users/%d/functions/%d", c.base, userID, fn.GetFunctionId())
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doFunction(req)
}

func (c *ControlPlaneClient) GetFunction(ctx context.Context, userID, functionID uint64) (*core.FunctionSpec, error) {
	url := fmt.Sprintf("%s/v1/users/%d/functions/%d", c.base, userID, functionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.doFunction(req)
}

func (c *ControlPlaneClient) DeleteFunction(ctx context.Context, userID, functionID uint64) error {
	url := fmt.Sprintf("%s/v1/users/%d/functions/%d", c.base, userID, functionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	_, err = c.do(req, http.StatusNoContent)
	if err == nil {
		c.log.Info("function deleted", "user_id", userID, "function_id", functionID)
	}
	return err
}

// PutPlatformConfig stores a complete platform-config document through the
// admin API. expectedVersion enables compare-and-swap; pass 0 to write
// unconditionally. It returns the stored document with its assigned version.
func (c *ControlPlaneClient) PutPlatformConfig(ctx context.Context, cfg *core.PlatformConfig, expectedVersion uint64) (*core.PlatformConfig, error) {
	body, err := protojson.Marshal(&controlplane.PutPlatformConfigRequest{
		Config:          cfg,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/v1/platform/config", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := c.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	stored := &core.PlatformConfig{}
	if err := protojson.Unmarshal(data, stored); err != nil {
		return nil, err
	}
	c.log.Info("controlplane platform config",
		"version", stored.GetVersion(),
		"routing", stored.GetRouting().GetPolicy(),
		"placement", stored.GetPlacement().GetPolicy(),
	)
	return stored, nil
}

func (c *ControlPlaneClient) doUser(req *http.Request) (*core.UserSpec, error) {
	data, err := c.do(req, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	user := &core.UserSpec{}
	if err := protojson.Unmarshal(data, user); err != nil {
		return nil, err
	}
	c.log.Info("controlplane user",
		"method", req.Method,
		"path", req.URL.Path,
		"user_id", user.GetUserId(),
		"name", user.GetName(),
	)
	return user, nil
}

func (c *ControlPlaneClient) doFunction(req *http.Request) (*core.FunctionSpec, error) {
	data, err := c.do(req, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	fn := &core.FunctionSpec{}
	if err := protojson.Unmarshal(data, fn); err != nil {
		return nil, err
	}
	c.log.Info("controlplane function",
		"method", req.Method,
		"path", req.URL.Path,
		"user_id", fn.GetUserId(),
		"function_id", fn.GetFunctionId(),
		"protocol", fn.GetRuntime().GetProtocol(),
		"image", fn.GetRuntime().GetImage(),
	)
	return fn, nil
}

func (c *ControlPlaneClient) do(req *http.Request, okStatus ...int) ([]byte, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if slices.Contains(okStatus, resp.StatusCode) {
		return data, nil
	}
	if len(data) > 0 {
		return nil, fmt.Errorf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(data))
	}
	return nil, fmt.Errorf("%s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
}
