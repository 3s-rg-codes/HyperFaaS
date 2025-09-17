package keyValueStore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

// HTTPClient can perform any http request
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// FunctionMetadataStore must provide a thread safe way to store and retrieve function metadata
type FunctionMetadataStore interface {
	// Put stores the function metadata in the store
	Put(image *common.Image, config *common.Config) (id string, err error)
	// Get retrieves the function metadata from the store
	Get(functionID string) (imageTag string, config *common.Config, err error)
}

// HttpDBClient provides a simple API wrapping http calls to the key value store server
type HttpDBClient struct {
	FunctionMetadataStore
	client  HTTPClient
	address string
	logger  *slog.Logger
}

// NewHttpDBClient creates a new HttpDBClient with a default http client
func NewHttpDBClient(address string, logger *slog.Logger) *HttpDBClient {
	return &HttpDBClient{
		address: address,
		client:  &http.Client{},
		logger:  logger,
	}
}

// NewHttpClientWithHTTPClient creates a new HttpDBClient. The httpClient must implement the HTTPClient interface
func NewHttpClientWithHTTPClient(address string, logger *slog.Logger, httpClient HTTPClient) *HttpDBClient {
	return &HttpDBClient{
		address: address,
		client:  httpClient,
		logger:  logger,
	}
}

type FunctionData struct {
	Config *common.Config
	Image  *common.Image
}

// Put creates a new function metadata entry in the key value store
func (db *HttpDBClient) Put(image *common.Image, config *common.Config) (string, error) {
	postData := PostRequest{
		ImageTag: image.Tag,
		Config: Config{
			CpuPeriod:      int(config.Cpu.Period),
			CpuQuota:       int(config.Cpu.Quota),
			MemLimit:       int(config.Memory),
			Timeout:        config.Timeout,
			MaxConcurrency: config.MaxConcurrency,
		},
	}

	jsonData, err := json.Marshal(postData)
	if err != nil {
		db.logger.Error("error marshaling JSON", "error", err)
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, db.address, bytes.NewBuffer(jsonData))
	if err != nil {
		db.logger.Error("error creating POST request", "error", err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := db.client.Do(req)
	if err != nil {
		db.logger.Error("error sending POST request", "error", err)
		return "", err
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			db.logger.Error("error closing the response body", "error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		db.logger.Error("POST request failed with status code", "error", resp.StatusCode)
		return "", fmt.Errorf("POST request failed with status code: %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var r PostResponse
	err = json.Unmarshal(b, &r)
	if err != nil {
		db.logger.Error("error unmarshaling json response", "error", err)
		return "", err
	}

	return r.FunctionID, nil
}

// Get gets the function metadata entry from the key value store by function ID
func (db *HttpDBClient) Get(functionID string) (string, *common.Config, error) {
	req, err := http.NewRequest(http.MethodGet, db.address, bytes.NewBuffer([]byte(functionID)))
	if err != nil {
		db.logger.Error("error creating GET request", "error", err)
		return "", &common.Config{}, err
	}

	resp, err := db.client.Do(req)
	if err != nil {
		db.logger.Error("error sending GET request", "error", err)
		return "", &common.Config{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", &common.Config{}, &NoSuchKeyError{Key: functionID}
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			db.logger.Error("error closing the response body", "error", err)
		}
	}(resp.Body)

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		db.logger.Error("error reading response", "error", err)
		return "", &common.Config{}, err
	}

	var r PostRequest
	err = json.Unmarshal(b, &r)
	if err != nil {
		db.logger.Error("error unmarshaling json", "error", err)
		return "", &common.Config{}, err
	}

	if resp.StatusCode != http.StatusOK {
		db.logger.Error("GET request failed with status code", "error", resp.StatusCode)
		return "", &common.Config{}, fmt.Errorf("GET request failed with status code: %d", resp.StatusCode)
	}

	c := &common.Config{
		Memory: int64(r.Config.MemLimit),
		Cpu: &common.CPUConfig{
			Period: int64(r.Config.CpuPeriod),
			Quota:  int64(r.Config.CpuQuota),
		},
		Timeout:        r.Config.Timeout,
		MaxConcurrency: r.Config.MaxConcurrency,
	}

	id := r.ImageTag

	return id, c, nil
}
