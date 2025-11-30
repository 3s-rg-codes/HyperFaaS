//go:build unit

package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/config"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/dataplane"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/metrics"
	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type MockWorkerClient struct{}

func (MockWorkerClient) Start(ctx context.Context, in *worker.StartRequest, opts ...grpc.CallOption) (*worker.StartResponse, error) {
	if in.FunctionId == "fail" {
		return &worker.StartResponse{}, errors.New("expected error")
	}
	return &worker.StartResponse{InstanceId: MOCK_INSTANCE_ID}, nil
}

func (MockWorkerClient) Stop(ctx context.Context, in *worker.StopRequest, opts ...grpc.CallOption) (*worker.StopResponse, error) {
	return &worker.StopResponse{InstanceId: in.InstanceId}, nil
}

func (MockWorkerClient) Status(ctx context.Context, in *worker.StatusRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[worker.StatusUpdate], error) {
	return nil, nil
}

func (MockWorkerClient) Metrics(ctx context.Context, in *worker.MetricsRequest, opts ...grpc.CallOption) (*worker.MetricsUpdate, error) {
	return nil, nil
}

func (MockWorkerClient) SignalReady(ctx context.Context, in *worker.SignalReadyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}

const (
	MOCK_WORKER_ID   = 0
	MOCK_FID         = "test"
	MOCK_FID_FAIL    = "fail"
	MOCK_INSTANCE_ID = "instance-id"

	TEST_CHANNEL_SIZE     = 100
	TEST_CHANNEL_TIMEOUTS = 1 * time.Second
	MAX_F_CONCURRENCY     = 1
)

func setup() *ControlPlane {

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	c := config.Config{}
	c.ApplyDefaults()

	instanceChangesChan := make(chan metrics.InstanceChange, TEST_CHANNEL_SIZE)
	metricsChan := make(chan metrics.MetricEvent, TEST_CHANNEL_SIZE)
	fse := make(chan metrics.ZeroScaleEvent, TEST_CHANNEL_SIZE)

	wCLient, err := createFakeWorker(ctx, c)
	if err != nil {
		panic("error creating mock worker")
	}

	workers := []*dataplane.WorkerClient{wCLient}

	cr := metrics.NewConcurrencyReporter(logger, metricsChan, TEST_CHANNEL_TIMEOUTS)
	cp := NewControlPlane(ctx, c, logger, instanceChangesChan, workers, fse, cr)

	fas1, err := createFunctionAutoscaler(c, wCLient, fse, cr, MOCK_FID)
	if err != nil {
		panic("error creating function autoscaler")
	}

	fas2, err := createFunctionAutoscaler(c, wCLient, fse, cr, MOCK_FID_FAIL)
	if err != nil {
		panic("error creating function autoscaler")
	}

	cp.functions[MOCK_FID] = fas1
	cp.functions[MOCK_FID_FAIL] = fas2

	return cp
}

func TestControlPlane_UpsertFunction(t *testing.T) {

	cp := setup()

	cp.UpsertFunction(&metadata.FunctionMetadata{ID: MOCK_FID})

	_, ok := cp.functions[MOCK_FID]

	assert.True(t, ok, "expected to find function ")
}

func TestControlPlane_FunctionExists(t *testing.T) {

	cp := setup()

	ok := cp.FunctionExists(MOCK_FID)
	notOk := cp.FunctionExists("anotherFunctionId")

	assert.True(t, ok, "function should exist but didnt")
	assert.False(t, notOk, "function should not exist, but did")

}

func TestControlPlane_RemoveFunction(t *testing.T) {

	cp := setup()

	_, ok := cp.functions[MOCK_FID]

	assert.True(t, ok, "function should exist after inserting")

	cp.RemoveFunction(MOCK_FID)

	_, ok = cp.functions[MOCK_FID]

	assert.False(t, ok, "function should not exist after deletion")

}

func TestControlPlane_RemoveFunctionIdempotent(t *testing.T) {

	cp := setup()

	for i := 0; i < 5; i++ {

		cp.RemoveFunction(MOCK_FID)
		_, ok := cp.functions[MOCK_FID]

		assert.False(t, ok, "function should not exist")

	}
}

// Checks if the order of actions performed on a function is correct at any point
func TestControlPlane_FunctionConsistency(t *testing.T) {

	cp := setup()

	numEvents := 100
	numFunctions := 10

	functionSnapshots := make([]map[string]int, 0)

	//simulate stream of events like it happens in controlplane from etcd
	for i := 0; i < numEvents; i++ {

		//perform an event on every function deterministically depending on the counters
		for j := 0; j < numFunctions; j++ {

			s := strconv.Itoa(j)

			cp.UpsertFunction(&metadata.FunctionMetadata{ID: s})

			if i*j%2 == 0 {

				cp.UpsertFunction(&metadata.FunctionMetadata{ID: s, Config: &common.Config{MaxConcurrency: 10}})

			} else {

				cp.RemoveFunction(s)

			}

		}

		//take a snapshot after a change has been made to all functions
		functionSnapshots = append(functionSnapshots, takeFunctionsSnapshot(cp.functions))

	}

	//iterate over the snapshots and check if the functions were in the desired state
	for i := 0; i < numEvents; i++ {

		for j := 0; j < numFunctions; j++ {

			conc, ok := functionSnapshots[i][strconv.Itoa(j)]

			if i*j%2 == 0 {

				assert.Equal(t, 10, conc, "expected to find changed value but did not")

			} else {

				assert.False(t, ok, "expected to not find function, but did")

			}
		}
	}
}

func TestControlPlane_HandleWorkerEvent(t *testing.T) {

	cp := setup()

	we := &dataplane.WorkerStatusEvent{
		Event:      worker.Event_EVENT_DOWN,
		FunctionId: MOCK_FID,
		InstanceId: MOCK_INSTANCE_ID,
	}

	cp.HandleWorkerEvent(MOCK_WORKER_ID, we)

	//two things happen: instance is removed from fas state and event is sent to fas changes channel

	fas := cp.functions[MOCK_FID]

	b := len(fas.workerStates[0].instances) == 0
	assert.True(t, b, "expected no function instances but there are instance(s)")

	select {
	case l := <-fas.instanceChangesChan:
		assert.Equal(t, "test", l.FunctionId, "instance of unexpected function was deleted")

		assert.False(t, l.Have, "expected function to be deleted but was created")
	case <-time.After(TEST_CHANNEL_TIMEOUTS):
		t.Error("message took too long to arrive")
	}

}

func TestControlPlane_Run(t *testing.T) {

	cp := setup()
	wipeInstances(cp)
	go cp.Run(cp.ctx)

	mc := cp.concurrencyReporter.GetMetricChan()

	//we send a coldstart event to the metrics channel
	e := metrics.MetricEvent{
		FunctionId:  MOCK_FID,
		Concurrency: 1,
		ColdStart:   true,
	}

	fas := cp.functions[MOCK_FID]
	currentTs := fas.lastRequestTimestamp
	currentFlight := fas.inFlight.Load()

	mc <- e

	//four things should happen:
	//	- instance change event is emitted
	//	- instance is appended for fas and widx
	// 	- timestamp of last request and fas inFlight counter are updated
	//  (- for coldstarts -> coldstart event is emitted)

	//assert that right event arrives
	select {
	case ice := <-fas.instanceChangesChan:
		assert.Equal(t, MOCK_FID, ice.FunctionId, "unexpected 'instance start event' was started")
		assert.True(t, ice.Have, "expected 'instance started event' to be emitted, got 'instance stopped' event")
	case <-time.After(1 * time.Second):
		t.Error("timeout for instance change event ran out")
	}

	//assert that expected instance was added
	inst := fas.workerStates[MOCK_WORKER_ID].instances[0]
	assert.Equal(t, MOCK_INSTANCE_ID, inst.id, "unexpected instance was started")

	//assert that timestamp was updated correctly
	newTs := fas.lastRequestTimestamp
	assert.False(t, newTs == currentTs, "expected 'last used' timestamp to be updated")

	//assert for flight counter: new = old + 1
	newFlight := fas.inFlight.Load()
	assert.Equal(t, currentFlight+1, newFlight, "expected one (1) new instance to be in flight")

	//we have coldstart here: assert that correct coldstart event was sent
	select {
	case ze := <-fas.zeroScaleChan:
		assert.Equal(t, MOCK_FID, ze.FunctionId, "unexpected functionId for coldstart event")
		assert.True(t, ze.Have, "expected scaling from 0 to 1, got 1 to 0")
	case <-time.After(TEST_CHANNEL_TIMEOUTS):
		t.Error("timeout ran out for receiving coldstart event")
	}

}

func Test_ScaleUpNoWorkers(t *testing.T) {

	cp := setup()
	autoscaler := cp.functions[MOCK_FID]

	//remove workers
	autoscaler.workers = make([]*dataplane.WorkerClient, 0)

	err := autoscaler.scaleUp(cp.ctx, MOCK_WORKER_ID, "reason")
	assert.NotNil(t, err, "expected an error to occur when no workers are registered for function")

}

func Test_ScaleUpWorkerError(t *testing.T) {

	cp := setup()
	autoscaler := cp.functions[MOCK_FID_FAIL]

	err := autoscaler.scaleUp(cp.ctx, 0, "reason")
	assert.NotNil(t, err, "expected error to not be nil when worker fails")

}

func Test_ScaleUp(t *testing.T) {

	cp := setup()
	autoscaler := cp.functions[MOCK_FID]
	wipeInstances(cp)

	iter := 10
	//scale up 'iter' times: first should be a coldstart, iterations afterwards shouldn't
	for i := 0; i < iter; i++ {
		oldWs := autoscaler.workerStates[0]
		err := autoscaler.scaleUp(cp.ctx, 0, "reason")
		if i >= autoscaler.maxInstancesPerWorker && err == nil {
			t.Error("unexpectedly no error occurred")
			return
		} else if i >= autoscaler.maxInstancesPerWorker {
			assert.Equal(t, errNoCapacity.Error(), err.Error(), "unexpected error occurred")
			continue
		}

		select {
		case ev := <-autoscaler.instanceChangesChan:
			assert.Equal(t, ev.FunctionId, MOCK_FID, "unexpected function id in event")
		case <-time.After(TEST_CHANNEL_TIMEOUTS):
			t.Error("timeout ran out for instance change event")
		}

		newWs := autoscaler.workerStates[0]
		assert.Equal(t, len(oldWs.instances)+1, len(newWs.instances), "expected one instance to be started")

		select {
		case ev := <-autoscaler.zeroScaleChan:
			if i != 0 {
				t.Error("received unexpected coldstart event")
			}
			assert.Equal(t, ev.FunctionId, MOCK_FID, "unexpected function id in event")
			assert.True(t, ev.Have, "expected scaling from 0 to 1, got 1 to 0")
		case <-time.After(TEST_CHANNEL_TIMEOUTS):
			if i == 0 {
				//fail when first start isnt cold
				t.Error("timeout ran out fr cold start event")
			}
		}
	}

}

func TestFunctionAutoScaler_AutoScaleToZero(t *testing.T) {
	cp := setup()
	autoscaler := cp.functions[MOCK_FID]
	wipeInstances(cp)

	go autoscaler.AutoScale()

	start := time.Now()
	autoscaler.lastRequestTimestamp = start

	err := autoscaler.scaleUp(cp.ctx, 0, "reason")
	assert.Nil(t, err, "unexpected error scaling up")

	startEv := <-autoscaler.zeroScaleChan
	assert.True(t, startEv.Have, "expected to receive start event")

	gracePeriod := autoscaler.scaleToZeroAfter + 1500*time.Millisecond

	select {
	case ev := <-autoscaler.zeroScaleChan:
		end := time.Now()
		period := end.Sub(start)
		assert.True(t, period < gracePeriod, "expected function to be stopped inside grace period")
		assert.False(t, ev.Have, "expected scaling to zero, but scaled to 1")
		assert.True(t, len(autoscaler.workerStates[0].instances) == 0, "expected no instances for function on worker")
	case <-time.After(gracePeriod):
		t.Error("did not scale to zero after")
	}

}

func TestFunctionAutoScaler_AutoScaleLoadNoInstances(t *testing.T) {

	cp := setup()
	autoscaler := cp.functions[MOCK_FID]
	wipeInstances(cp)

	//add synthetic requests to that load > 0
	autoscaler.inFlight.Store(1)

	go autoscaler.AutoScale()
	grace := TEST_CHANNEL_TIMEOUTS + 500*time.Millisecond

	wg := sync.WaitGroup{}

	wg.Go(func() {
		select {
		case ev := <-autoscaler.instanceChangesChan:
			assert.True(t, ev.Have, "unexpected scale down event")
		case <-time.After(grace): //the scaler takes 1 second before checking the first time, give it extra time
			t.Error("timeout for instance change event ran out")
		}
	})

	wg.Go(func() {
		select {
		case ev := <-autoscaler.zeroScaleChan:
			assert.True(t, ev.Have, "unexpected scale down event")
		case <-time.After(grace): //the scaler takes 1 second before checking the first time, give it extra time
			t.Error("timeout for instance change event ran out")
		}
	})

	wg.Wait()

	assert.True(t, len(autoscaler.workerStates[0].instances) == 1, "unexpected amount of instances")

}

func TestFunctionAutoScaler_AutoScaleNotEnoughInstances(t *testing.T) {

	cp := setup()
	autoscaler := cp.functions[MOCK_FID]

	//we only have one worker so set the load accordingly
	generatedLoad := (autoscaler.maxInstancesPerWorker) * autoscaler.maxConcurrencyPerInstance
	autoscaler.inFlight.Store(int64(generatedLoad))

	go autoscaler.AutoScale()
	grace := TEST_CHANNEL_TIMEOUTS + 500*time.Millisecond

	wg := sync.WaitGroup{}

	wg.Go(func() {
		//one instance is created during setup
		for i := 0; i < autoscaler.maxInstancesPerWorker-1; i++ {
			select {
			case ev := <-autoscaler.instanceChangesChan:
				assert.True(t, ev.Have, "unexpected scale down event")
			case <-time.After(grace):
				t.Error("timeout for instance change event ran out")
			}
		}
	})

	wg.Go(func() {
		for i := 0; i < generatedLoad; i++ {
			select {
			case <-autoscaler.zeroScaleChan:
				t.Error("received unexpected zero scale event")
			case <-time.After(grace):
				continue
			}
		}
	})

	wg.Wait()

	assert.Equal(t, autoscaler.maxInstancesPerWorker, len(autoscaler.workerStates[0].instances))

}

func takeFunctionsSnapshot(m map[string]*functionAutoScaler) map[string]int {

	mapCopy := make(map[string]int)
	for k, v := range m {
		mapCopy[k] = v.maxConcurrencyPerInstance
	}
	return mapCopy
}

func createFakeWorker(ctx context.Context, c config.Config) (*dataplane.WorkerClient, error) {

	return dataplane.NewMockWorkerClient(
		ctx,
		0,
		"addr",
		c,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
	)
}

// for some tests, a few things need to be set in the nested struct, but fields that do not need to be set are not set here
func createFunctionAutoscaler(
	cfg config.Config,
	w *dataplane.WorkerClient,
	zeroScaleCh chan metrics.ZeroScaleEvent,
	reporter *metrics.ConcurrencyReporter,
	fId string,
) (*functionAutoScaler, error) {

	scaler := newFunctionAutoScaler(context.Background(),
		fId,
		&common.Config{MaxConcurrency: MAX_F_CONCURRENCY},
		make(chan metrics.InstanceChange, 10),
		[]*dataplane.WorkerClient{w},
		cfg,
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
		zeroScaleCh,
		reporter,
	)

	inst := instance{
		id:        MOCK_INSTANCE_ID,
		ip:        "ip",
		lastUsage: time.Now().Add(-time.Minute),
	}

	scaler.totalInstances++
	scaler.workerStates[0].instances = append(scaler.workerStates[0].instances, inst)

	return scaler, nil

}

func wipeInstances(cp *ControlPlane) {
	for i := range cp.functions {
		v := cp.functions[i]
		v.totalInstances = 0
		for j := range v.workerStates {
			v.workerStates[j].instances = make([]instance, 0)
		}
	}
}
