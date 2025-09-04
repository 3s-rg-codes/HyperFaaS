package keyValueStore

import (
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

const ADDRESS = "http://localhost:8080/"

var (
	sampleData map[string]PostRequest
	idList     []string
)

func TestMain(m *testing.M) {
	setup()
	m.Run()
}

func setup() {
	fmt.Println("Starting http server")
	db := Store{
		Lock: sync.RWMutex{},
		Data: make(map[string]PostRequest),
	}
	go db.Start()

	// Write some sample data
	entries := 100

	sampleData = make(map[string]PostRequest)
	// for testing purposes image tag AND function ID are UUIDS
	func() {
		for i := 0; i < entries; i++ {
			functionID := uuid.New().String() // create random id
			idList = append(idList, functionID)

			sampleData[functionID] = generateRandomConfig() // create random data
		}
	}()

	db.Data = sampleData
}

func TestHttpDBClient_Get(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dbClient := NewHttpClient(ADDRESS, logger)

	id := idList[50]
	data := sampleData[id]

	tag, c, err := dbClient.Get(id)
	if err != nil {
		t.Errorf("error calling GET, %v", err)
	}

	assert.True(t, fieldsEqual(data, tag, c), "expected and actual config don't match")
}

func TestHttpDBClient_Put(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dbClient := NewHttpClient(ADDRESS, logger)

	id := idList[30]
	postRequest := sampleData[id]

	parsedConfig := &common.Config{
		Memory: int64(postRequest.Config.MemLimit),
		Cpu: &common.CPUConfig{
			Period: int64(postRequest.Config.CpuPeriod),
			Quota:  int64(postRequest.Config.CpuQuota),
		},
	}

	functionID, err := dbClient.Put(&common.Image{Tag: postRequest.ImageTag}, parsedConfig)
	if err != nil {
		t.Errorf("error calling PUT, %v", err)
	}

	assert.True(t, uuid.Validate(functionID) == nil, "returned non-valid function id")
}

func generateRandomConfig() PostRequest {
	return PostRequest{
		Config: Config{
			MemLimit:  rand.Intn(9001) + 1000,   // 1000 - 10000
			CpuPeriod: rand.Intn(3001) + 2000,   // 2000 - 5000
			CpuQuota:  rand.Intn(30001) + 20000, // 20000 - 50000
		},
		ImageTag: uuid.New().String(),
	}
}

func fieldsEqual(data PostRequest, tag string, c *common.Config) bool {
	if data.ImageTag != tag {
		return false
	}
	if data.Config.CpuQuota != int(c.Cpu.Quota) {
		return false
	}
	if data.Config.CpuPeriod != int(c.Cpu.Period) {
		return false
	}
	if data.Config.MemLimit != int(c.Memory) {
		return false
	}
	return true
}
