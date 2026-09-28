package dataplane

import "time"

// Function is the immutable request-facing record for one deployed function.
// Pool is the only mutable object reached by an invocation.
type Function struct {
	ID             uint64
	Protocol       string
	RequestTimeout time.Duration
	Pool           *SandboxPool
}
