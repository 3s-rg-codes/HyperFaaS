package autoscaling

import "time"

// Signals are the local inputs a scaling policy reads each reconcile tick.
type Signals struct {
	ReadyInstances uint32
	PendingStarts  uint32
	PendingStops   uint32
	// LogicalScale is Dirigent's atomically updated desired scale. It is set
	// only after a strict actuator decision, so pending starts are already
	// represented and must not be added again.
	LogicalScale         uint64
	HasLogicalScale      bool
	AvailableConcurrency uint64
	InFlight             uint64
	// HighWater is the maximum in-flight demand retained since the previous
	// controller decision. It is used only as a scale-up floor.
	HighWater      uint64
	Executing      uint64
	QueueDepth     uint64
	OldestQueueAge time.Duration
	LastActivity   time.Time
}
