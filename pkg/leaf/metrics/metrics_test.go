package metrics

import (
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	METRIC_CHAN_BUFFER = 10000
	REPORT_INTERVAL    = 1 * time.Second
	TEST_FUNCTION      = "test"
	ITERATIONS         = 100
	TIMEOUT            = 500 * time.Millisecond
	RAND_SEED          = 42
)

func setup() *ConcurrencyReporter {

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	metricChan := make(chan MetricEvent, METRIC_CHAN_BUFFER)

	cr := NewConcurrencyReporter(logger, metricChan, REPORT_INTERVAL)

	return cr
}

func TestConcurrencyReporter_HandleRequestInExisting(t *testing.T) {

	cr := setup()

	stat := &functionStats{inFlight: atomic.Int64{}}
	stat.inFlight.Add(1)

	cr.stats[TEST_FUNCTION] = stat

	for i := 0; i < ITERATIONS; i++ {
		cr.HandleRequestIn(TEST_FUNCTION)
	}

	assert.Equal(t, int64(ITERATIONS+1), cr.stats[TEST_FUNCTION].inFlight.Load(), "unexpected number of functions are in flight")

}

func TestConcurrencyReporter_HandleRequestInNew(t *testing.T) {

	cr := setup()

	cr.HandleRequestIn(TEST_FUNCTION)

	assert.Equal(t, int64(1), cr.stats[TEST_FUNCTION].inFlight.Load(), "unexpectedly found no inFlight instance")

	select {
	case ev := <-cr.metricChan:
		assert.Equal(t, TEST_FUNCTION, ev.FunctionId, "unexpected cold start event was sent for function")
		assert.True(t, ev.ColdStart, "unexpectedly new function was not cold start")
	case <-time.After(TIMEOUT):
		t.Error("timeout for metric channel ran out")
	}

}

func TestConcurrencyReporter_HandleRequestInExistingConcurrency(t *testing.T) {

	cr := setup()

	stat := &functionStats{inFlight: atomic.Int64{}}
	stat.inFlight.Add(1)

	cr.stats[TEST_FUNCTION] = stat

	wg := sync.WaitGroup{}

	//ensure concurrency behavior is always the same
	rand.New(rand.NewSource(RAND_SEED))

	for i := 0; i < ITERATIONS; i++ {
		wg.Go(func() {
			//sleep for "random" time
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			cr.HandleRequestIn(TEST_FUNCTION)
		})
	}

	wg.Wait()

	assert.Equal(t, int64(ITERATIONS+1), cr.stats[TEST_FUNCTION].inFlight.Load(), "unexpected number of functions are in flight")

}

func TestConcurrencyReporter_HandleRequestInNewConcurrency(t *testing.T) {

	cr := setup()

	wg := sync.WaitGroup{}

	//ensure concurrency behavior is always the same
	rand.New(rand.NewSource(RAND_SEED))

	for i := 0; i < ITERATIONS; i++ {
		wg.Go(func() {
			//sleep for "random" time
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			cr.HandleRequestIn(TEST_FUNCTION + " " + strconv.Itoa(i))
		})
	}

	wg.Wait()

	for _, v := range cr.stats {

		assert.Equal(t, int64(1), v.inFlight.Load(), "unexpectedly found more than one instance for a function")

	}
}

func TestConcurrencyReporter_HandleRequestOut(t *testing.T) {

	cr := setup()

	stat := &functionStats{inFlight: atomic.Int64{}}
	stat.inFlight.Add(1)

	cr.stats[TEST_FUNCTION] = stat

	cr.HandleRequestOut(TEST_FUNCTION)

	assert.Equal(t, int64(0), cr.stats[TEST_FUNCTION].inFlight.Load(), "expected no instances running but %v were running", cr.stats[TEST_FUNCTION].inFlight.Load())

}

// for a non-existent function outgoing request does nothing
func TestConcurrencyReporter_HandleRequestOutNonExistent(t *testing.T) {

	cr := setup()

	cr.HandleRequestOut(TEST_FUNCTION)

	assert.True(t, len(cr.stats) == 0, "found unexpected function in stats list")

}

func TestConcurrencyReporter_InAndOut(t *testing.T) {

	cr := setup()

	//ensure concurrency behavior is always the same
	rand.New(rand.NewSource(RAND_SEED))

	valsX := make([]float64, 0)
	valsY := make([]float64, 0)

	expected := make(map[string]int64)

	valsC := make([]int, 0)

	c := 0
	for i := 0; i < ITERATIONS; i++ {
		x := rand.Float64()
		valsX = append(valsX, x)
		y := rand.Float64()
		valsY = append(valsY, y)

		//if that's the case, create a new function
		if y > 0.5 {
			c++
		}

		valsC = append(valsC, c)

		name := TEST_FUNCTION + " " + strconv.Itoa(c)

		if x > 0.5 {
			cr.HandleRequestIn(name)
			expected[name] = expected[name] + 1
		} else {
			cr.HandleRequestOut(name)
			if _, ok := cr.stats[name]; ok {
				expected[name] = expected[name] - 1
			}
		}
	}

	for k, v := range expected {
		assert.Equal(t, v, cr.stats[k].inFlight.Load(), "unexpected number of requests for %s", k)
	}

}
