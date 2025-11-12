package metrics

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Based on knative activator ConcurrencyReporter.

// ConcurrencyReporter reports stats based on incoming reqs.
type ConcurrencyReporter struct {
	logger *slog.Logger
	mu     sync.RWMutex

	// where we send metric events to.
	metricChan chan MetricEvent

	// functionId -> how many requests are currently in flight in total for this function.
	stats map[string]*functionStats

	// in Knative, a report is sent every second.
	reportInterval time.Duration
}

func NewConcurrencyReporter(logger *slog.Logger, metricChan chan MetricEvent, reportInterval time.Duration) *ConcurrencyReporter {
	return &ConcurrencyReporter{
		metricChan:     metricChan,
		reportInterval: reportInterval,
		stats:          make(map[string]*functionStats),
		logger:         logger.With("component", "concurrency_reporter"),
	}
}

type functionStats struct {
	// how many requests are currently in flight.
	inFlight atomic.Int64
}

// MetricEvent is a message used to notify components of changes in the concurrency of a function.
type MetricEvent struct {
	FunctionId  string
	Concurrency int64
	ColdStart   bool
}

func (c *ConcurrencyReporter) HandleRequestIn(functionId string) {
	c.mu.RLock()
	stat := c.stats[functionId]
	c.mu.RUnlock()
	if stat != nil {
		stat.inFlight.Add(1)
		return
	}

	c.mu.Lock()

	stat = c.stats[functionId]
	if stat != nil {
		c.mu.Unlock()
		stat.inFlight.Add(1)
		return
	}

	stat = &functionStats{
		inFlight: atomic.Int64{},
	}
	stat.inFlight.Add(1)

	c.stats[functionId] = stat
	c.mu.Unlock()

	// we HAVE to send the event now for the cold start to be handled as soon as possible
	// by the ControlPlane, which will then scale up the function.

	c.logger.Debug("AAA sending cold start metric event", "function_id", functionId)
	c.metricChan <- MetricEvent{
		FunctionId:  functionId,
		Concurrency: 1,
		ColdStart:   true,
	}
}

func (c *ConcurrencyReporter) HandleRequestOut(functionId string) {

	c.mu.RLock()
	stat := c.stats[functionId]
	if stat == nil {
		return
	}
	c.mu.RUnlock()
	stat.inFlight.Add(-1)
}

// For scale to zero, we need to delete the function stats so that the next request will trigger a cold start.
func (c *ConcurrencyReporter) DeleteFunctionStats(functionId string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.stats, functionId)
}

func (c *ConcurrencyReporter) GetMetricChan() chan MetricEvent {
	return c.metricChan
}

func (c *ConcurrencyReporter) Run(ctx context.Context) {
	t := time.NewTicker(c.reportInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.report()
		}
	}
}

// send state of all functions to the metric channel.
func (c *ConcurrencyReporter) report() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for functionId, stat := range c.stats {
		//c.logger.Debug("reporting metric", "function_id", functionId, "concurrency", stat.inFlight.Load())

		go func(fid string, conc int64) {
			c.metricChan <- MetricEvent{
				FunctionId:  fid,
				Concurrency: conc,
			}
		}(functionId, stat.inFlight.Load())
	}
}
