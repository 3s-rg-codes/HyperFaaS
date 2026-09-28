package autoscaling

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"hyperfaas-ideal-arch/pkg/core"
	"hyperfaas-ideal-arch/pkg/leaf/dataplane"
	leafworker "hyperfaas-ideal-arch/pkg/leaf/worker"
)

func benchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newReconcileBench builds `functions` actuators, each tracking `perFunction`
// instances on worker 0, plus the matching full WorkerState snapshot. Instance
// IDs are unique across functions, mirroring leaf-assigned IDs.
func newReconcileBench(functions, perFunction int) ([]*SandboxActuator, *core.WorkerState) {
	const baseID = uint64(1) << 60
	actuators := make([]*SandboxActuator, 0, functions)
	states := make([]*core.InstanceState, 0, functions*perFunction)
	id := baseID
	for f := 0; f < functions; f++ {
		functionID := uint64(f + 1)
		actuator := NewSandboxActuator(ActuatorConfig{
			FunctionID: functionID,
			Logger:     benchLogger(),
			Workers:    make([]*leafworker.Client, 1),
			Store:      dataplane.NewStore(),
			Pool:       dataplane.NewSandboxPool(1, 1),
			Reporter:   discardPublisher{},
		})
		for i := 0; i < perFunction; i++ {
			id++
			actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
				id:                  id,
				address:             "10.0.0.5:1",
				workerStateRevision: 1,
			})
			actuator.totalInstances++
			states = append(states, &core.InstanceState{InstanceId: id, FunctionId: functionID})
		}
		actuators = append(actuators, actuator)
	}
	return actuators, &core.WorkerState{SandboxRevision: 1000, SandboxStates: states}
}

// BenchmarkReconcileWorkerState stresses the periodic relist across the bench
// envelope. It compares building one shared presence per snapshot against the
// previous per-function rebuild that was O(functions x instances).
func BenchmarkReconcileWorkerState(b *testing.B) {
	for _, functions := range []int{1, 10, 30} {
		for _, perFunction := range []int{10, 100, 1000} {
			name := fmt.Sprintf("functions=%d/instances_per_function=%d", functions, perFunction)

			b.Run(name+"/shared_presence", func(b *testing.B) {
				actuators, state := newReconcileBench(functions, perFunction)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					presence := NewWorkerPresence(state)
					for _, actuator := range actuators {
						actuator.ReconcileWorkerState(0, presence)
					}
				}
			})

			b.Run(name+"/per_function_presence", func(b *testing.B) {
				actuators, state := newReconcileBench(functions, perFunction)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					for _, actuator := range actuators {
						actuator.ReconcileWorkerState(0, NewWorkerPresence(state))
					}
				}
			})
		}
	}
}

// BenchmarkHandleSandboxRemoved measures the low-latency event path. It stays
// proportional to that function's tracked instances, independent of how many
// other instances the worker reports.
func BenchmarkHandleSandboxRemoved(b *testing.B) {
	const baseID = uint64(1) << 60
	for _, perFunction := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("instances=%d", perFunction), func(b *testing.B) {
			actuators, _ := newReconcileBench(1, perFunction)
			actuator := actuators[0]
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := baseID + uint64(i%perFunction+1)
				actuator.HandleSandboxRemoved(0, id, 1000)
				actuator.mu.Lock()
				actuator.workerInstances[0].instances = append(actuator.workerInstances[0].instances, trackedInstance{
					id:                  id,
					address:             "10.0.0.5:1",
					workerStateRevision: 1,
				})
				actuator.totalInstances++
				actuator.mu.Unlock()
			}
		})
	}
}
