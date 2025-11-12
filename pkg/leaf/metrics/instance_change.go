package metrics

// InstanceChange is a message used to notify components of changes in the instance count of a function.
type InstanceChange struct {
	FunctionId string
	Address    string
	// true if created, false if destroyed
	Have bool
}

// ZeroScaleEvent is a message used to serve the State stream,
// consumed by upstream nodes that need to know if this leaf manages instances for a function.
type ZeroScaleEvent struct {
	FunctionId string
	Zero       bool
}
