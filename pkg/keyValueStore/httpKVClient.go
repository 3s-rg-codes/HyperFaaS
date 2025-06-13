package keyValueStore

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type HttpDBClient struct {
	FunctionMetadataStore
	client  http.Client
	address string
	logger  *slog.Logger
}

func NewHttpClient(address string, logger *slog.Logger) *HttpDBClient {
	return &HttpDBClient{
		address: address,
		client:  http.Client{},
		logger:  logger,
	}
}

type FunctionData struct {
	Config   *common.Config
	ImageTag *common.ImageTag
}

// Put is called Put because we use it as a key-value-store, but from a REST POV its POST
func (db *HttpDBClient) Put(imageTag *common.ImageTag, config *common.Config) (*common.FunctionID, error) {

	postData := PostRequest{
		ImageTag: imageTag.Tag,
		Config: Config{
			CpuPeriod:      int(config.Cpu.Period),
			CpuQuota:       int(config.Cpu.Quota),
			MemLimit:       int(config.Memory),
			Timeout:        int32(config.Timeout),
			MaxConcurrency: int32(config.MaxConcurrency),
		},
	}

	jsonData, err := json.Marshal(postData)
	if err != nil {
		db.logger.Error("error marshaling JSON", "error", err)
		return &common.FunctionID{}, err
	}

	req, err := http.NewRequest(http.MethodPost, db.address, bytes.NewBuffer(jsonData))
	if err != nil {
		db.logger.Error("error creating POST request", "error", err)
		return &common.FunctionID{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := db.client.Do(req)
	if err != nil {
		db.logger.Error("error sending POST request", "error", err)
		return &common.FunctionID{}, err
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			db.logger.Error("error closing the response body", "error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		db.logger.Error("POST request failed with status code", "error", resp.StatusCode)
		return &common.FunctionID{}, err
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return &common.FunctionID{}, err
	}

	var r PostResponse
	err = json.Unmarshal(b, &r)
	if err != nil {
		db.logger.Error("error unmarshaling json response", "error", err)
		return &common.FunctionID{}, err
	}

	return &common.FunctionID{Id: r.FunctionID}, nil
}

func (db *HttpDBClient) Get(functionID *common.FunctionID) (*common.ImageTag, *common.Config, error) {

	req, err := http.NewRequest(http.MethodGet, db.address, bytes.NewBuffer([]byte(functionID.Id)))
	if err != nil {
		db.logger.Error("error creating GET request", "error", err)
		return &common.ImageTag{}, &common.Config{}, err
	}

	resp, err := db.client.Do(req)
	if err != nil {
		db.logger.Error("error sending GET request", "error", err)
		return &common.ImageTag{}, &common.Config{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return &common.ImageTag{}, &common.Config{}, &NoSuchKeyError{Key: functionID}
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
		return &common.ImageTag{}, &common.Config{}, err
	}

	var r PostRequest
	err = json.Unmarshal(b, &r)
	if err != nil {
		db.logger.Error("error unmarshaling json", "error", err)
		return &common.ImageTag{}, &common.Config{}, err
	}

	if resp.StatusCode != http.StatusOK {
		db.logger.Error("GET request failed with status code", "error", resp.StatusCode)
		return &common.ImageTag{}, &common.Config{}, err
	}

	c := &common.Config{
		Memory: int64(r.Config.MemLimit),
		Cpu: &common.CPUConfig{
			Period: int64(r.Config.CpuPeriod),
			Quota:  int64(r.Config.CpuQuota),
		},
		Timeout:        int32(r.Config.Timeout),
		MaxConcurrency: int32(r.Config.MaxConcurrency),
	}

	id := &common.ImageTag{Tag: r.ImageTag}

	return id, c, nil
}
