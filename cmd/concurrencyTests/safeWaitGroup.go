package concurrencyTests

import (
	"sync"
)

type SafeWaitGroup struct {
	wg sync.WaitGroup
	mu sync.Mutex
	n  int
}

func (swg *SafeWaitGroup) Add(delta int) {
	swg.mu.Lock()
	defer swg.mu.Unlock()
	swg.n += delta
	swg.wg.Add(delta)
}

func (swg *SafeWaitGroup) Done() {
	swg.mu.Lock()
	defer swg.mu.Unlock()
	swg.n--
	swg.wg.Done()
}

func (swg *SafeWaitGroup) Wait() {
	swg.wg.Wait()
}

func (swg *SafeWaitGroup) Reset() {
	swg.mu.Lock()
	defer swg.mu.Unlock()
	for swg.n > 0 {
		swg.n--
		swg.wg.Done()
	}
}

func (swg *SafeWaitGroup) IsZero() bool {
	swg.mu.Lock()
	defer swg.mu.Unlock()
	return swg.n == 0
}
