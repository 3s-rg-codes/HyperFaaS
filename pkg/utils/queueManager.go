package utils

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

type QueueManager struct {
	cr     *metrics.ConcurrencyReporter
	ctx    context.Context
	cq     *callQueue
	cc     chan *common.CallRequest
	logger *slog.Logger
}

func NewQueueManager(ctx context.Context, cr *metrics.ConcurrencyReporter, callChan chan *common.CallRequest, logger *slog.Logger) *QueueManager {
	cq := newCallQueue()

	qm := &QueueManager{
		ctx:    ctx,
		cr:     cr,
		cq:     cq,
		cc:     callChan,
		logger: logger,
	}

	go qm.run()

	return qm
}

func (qm *QueueManager) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	loggerCounter := 0
	qm.logger.Info("QueueManager started")

	for {
		select {
		case <-qm.ctx.Done():
			qm.logger.Info("QueueManager stopping due to context cancellation")
			return

		case <-ticker.C:
			loggerCounter = (loggerCounter + 1) % 10
			if loggerCounter == 0 {
				qm.logger.Info("QueueManager heartbeat")
			}

			available := checkAvailability(qm.cr)
			empty := qm.cq.isEmpty()

			qm.logger.Debug(fmt.Sprintf("QueueManager: Availability=%v, Empty=%v", available, empty))

			if !available || empty {
				qm.logger.Debug("QueueManager: No calls to forward.")
				continue
			}

			req, err := qm.Dequeue()
			if err != nil {
				qm.logger.Error("QueueManager: Error dequeueing request", "error", err)
				continue
			}

			req.Async = false
			qm.logger.Info(fmt.Sprintf("QueueManager: Forwarding request fID=%v", req.FunctionId))
			qm.cc <- req
		}
	}
}

type callQueue struct {
	mu sync.RWMutex
	q  []*common.CallRequest
}

func newCallQueue() *callQueue {
	return &callQueue{
		q: make([]*common.CallRequest, 0),
	}
}

func (qm *QueueManager) Enqueue(req *common.CallRequest) error {
	qm.logger.Info(fmt.Sprintf("QueueManager: Enqueued request for fID: %v", req.FunctionId))
	qm.cq.mu.Lock()
	defer qm.cq.mu.Unlock()
	qm.cq.q = append(qm.cq.q, req)
	qm.logger.Info(fmt.Sprintf("QueueManager: Enqueued request for fID: %v", req.FunctionId))
	return nil
}

func (qm *QueueManager) Dequeue() (*common.CallRequest, error) {
	qm.cq.mu.Lock()
	defer qm.cq.mu.Unlock()

	if len(qm.cq.q) == 0 {
		return nil, fmt.Errorf("Nothing to dequeue, queue is empty!")
	}

	req := qm.cq.q[0]
	qm.cq.q = qm.cq.q[1:]

	qm.logger.Info(fmt.Sprintf("QueueManager: Dequeued request for fID: %v", req.FunctionId))

	return req, nil
}

// returns true if queue is empty
func (cq *callQueue) isEmpty() bool {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	return len(cq.q) == 0
}

func checkAvailability(cr *metrics.ConcurrencyReporter) bool {
	// TODO: implement availability check
	cr.GetMetricChan()
	return true
}
