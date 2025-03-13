package api

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"sync"
	"testing"
	"time"
)

// Global counter for created connections
var createCount int
var createCountMutex sync.Mutex

func mockFactory() (*grpc.ClientConn, error) {
	// Lock to safely increment count across goroutines
	createCountMutex.Lock()
	createCount++
	createCountMutex.Unlock()

	// Instead of creating a real connection, return a manually created ClientConn
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}
	return conn, nil
}

func TestConnectionPoolingConcurrent(t *testing.T) {
	// Reset counter before test
	createCount = 0

	// Create a new PoolManager
	poolManager := NewPoolManager()

	// Worker ID we are testing
	workerID := "localhost:50051"

	// Get a pool for this worker
	pool, err := poolManager.GetPool(workerID, mockFactory)
	assert.NoError(t, err)

	// Number of concurrent calls
	const numGoroutines = 10

	// Use a WaitGroup to wait for all Goroutines to complete
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Run multiple concurrent requests
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Get a connection from the pool
			conn, err := pool.Get(context.Background())
			assert.NoError(t, err)
			assert.NotNil(t, conn)

			// Simulate some work with the connection
			time.Sleep(10 * time.Millisecond)

			// Return the connection to the pool
			conn.Close()
		}(i)
	}

	// Wait for all Goroutines to finish
	wg.Wait()

	// Ensure that the number of created connections does not exceed the max pool size (7)
	assert.LessOrEqual(t, createCount, 7, "More connections were created than expected")

	fmt.Printf("Total connections created: %d\n", createCount)
}
