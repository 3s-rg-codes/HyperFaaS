//go:build unit

package keyValueStore

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockHTTPClient struct {
	DoFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return nil, nil
}

// Helper function to create mock responses
func NewMockResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func TestHttpDBClient_Put_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	expectedFunctionID := uuid.New().String()

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "application/json", req.Header.Get("Content-Type"))

			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)

			var postData PostRequest
			err = json.Unmarshal(body, &postData)
			require.NoError(t, err)
			assert.Equal(t, "test-image:latest", postData.ImageTag)
			assert.Equal(t, 1024, postData.Config.MemLimit)
			assert.Equal(t, 100000, postData.Config.CpuQuota)
			assert.Equal(t, 100000, postData.Config.CpuPeriod)

			response := PostResponse{FunctionID: expectedFunctionID}
			responseBody, _ := json.Marshal(response)
			return NewMockResponse(http.StatusCreated, string(responseBody)), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)
	image := &common.Image{Tag: "test-image:latest"}
	config := &common.Config{
		Memory: 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
		Timeout:        30,
		MaxConcurrency: 10,
	}

	functionID, err := client.Put(image, config)

	require.NoError(t, err)
	assert.Equal(t, expectedFunctionID, functionID)
}

func TestHttpDBClient_Put_NetworkError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return nil, assert.AnError
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)
	image := &common.Image{Tag: "test-image:latest"}
	config := &common.Config{
		Memory: 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}

	functionID, err := client.Put(image, config)

	require.Error(t, err)
	assert.Empty(t, functionID)
	assert.Equal(t, assert.AnError, err)
}

func TestHttpDBClient_Put_ServerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return NewMockResponse(http.StatusInternalServerError, ""), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)
	image := &common.Image{Tag: "test-image:latest"}
	config := &common.Config{
		Memory: 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}

	functionID, err := client.Put(image, config)

	require.Error(t, err)
	assert.Empty(t, functionID)
}

func TestHttpDBClient_Get_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	expectedImageTag := "test-image:latest"

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodGet, req.Method)

			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assert.Equal(t, "test-function-id", string(body))

			response := PostRequest{
				ImageTag: expectedImageTag,
				Config: Config{
					MemLimit:       1024,
					CpuQuota:       100000,
					CpuPeriod:      100000,
					Timeout:        30,
					MaxConcurrency: 10,
				},
			}
			responseBody, _ := json.Marshal(response)
			return NewMockResponse(http.StatusOK, string(responseBody)), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)

	imageTag, config, err := client.Get("test-function-id")

	require.NoError(t, err)
	assert.Equal(t, expectedImageTag, imageTag)
	assert.Equal(t, int64(1024), config.Memory)
	assert.Equal(t, int64(100000), config.Cpu.Period)
	assert.Equal(t, int64(100000), config.Cpu.Quota)
	assert.Equal(t, int32(30), config.Timeout)
	assert.Equal(t, int32(10), config.MaxConcurrency)
}

func TestHttpDBClient_Get_NotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return NewMockResponse(http.StatusNotFound, ""), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)

	imageTag, config, err := client.Get("non-existent-id")

	require.Error(t, err)
	assert.Empty(t, imageTag)
	assert.NotNil(t, config)
	assert.IsType(t, &NoSuchKeyError{}, err)
}

func TestHttpDBClient_Get_ServerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return NewMockResponse(http.StatusInternalServerError, ""), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)

	imageTag, config, err := client.Get("test-function-id")

	require.Error(t, err)
	assert.Empty(t, imageTag)
	assert.NotNil(t, config)
}

func TestHttpDBClient_Put_InvalidJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return NewMockResponse(http.StatusBadRequest, "invalid json"), nil
		},
	}

	client := NewHttpClientWithHTTPClient("", logger, mockClient)

	image := &common.Image{Tag: "test-image:latest"}
	config := &common.Config{
		Memory: 1024,
		Cpu: &common.CPUConfig{
			Period: 100000,
			Quota:  100000,
		},
	}

	functionID, err := client.Put(image, config)

	require.Error(t, err)
	assert.Empty(t, functionID)
}
