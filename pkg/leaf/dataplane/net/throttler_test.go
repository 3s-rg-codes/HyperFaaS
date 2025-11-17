//go:build unit

package net

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	containerConcurrency = 1000
	instanceCount        = 100
)

type testClient struct {
	counter atomic.Int32
}

func (c *testClient) call() {
	c.counter.Add(1)
}

func setup() (*Throttler, *testClient) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	throttler := NewThrottler(containerConcurrency, logger)
	client := &testClient{
		counter: atomic.Int32{},
	}
	return throttler, client
}

func TestThrottlerWithAvailableInstances(t *testing.T) {
	throttler, client := setup()

	for i := range instanceCount {
		throttler.AddInstance(fmt.Sprintf("127.0.0.1:808%d", i))
	}

	for range 1000 {
		throttler.Try(context.Background(), func(address string) error {
			client.call()
			return nil
		})
	}

	if client.counter.Load() != 1000 {
		t.Errorf("expected 1000 calls, got %d", client.counter.Load())
	}
}

// add and remove instances randomly, call Try
func TestThrottlerFuzz(t *testing.T) {
	throttler, client := setup()

	go func() {
		for range 1000 {
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
			ip := fmt.Sprintf("127.0.0.1:808%d", rand.Intn(10000))
			throttler.AddInstance(ip)
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
			throttler.RemoveInstance(ip)
		}
	}()

	wg := sync.WaitGroup{}

	wg.Add(1000)
	for range 1000 {
		go func(cl *testClient) {
			defer wg.Done()
			throttler.Try(context.Background(), func(address string) error {
				cl.call()
				return nil
			})
		}(client)
	}
	wg.Wait()

	if client.counter.Load() != 1000 {
		t.Errorf("expected 1000 calls, got %d", client.counter.Load())
	}
}
