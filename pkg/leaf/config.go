package leafv2

import "time"

type Config struct {
	// WorkerAddresses are the worker endpoints that the leaf manages.
	WorkerAddresses []string

	// Containerized is a flag to indicate if the leaf is running in a containerized environment.
	// Primarily affects wether to use the internal or external IP of function instances to call them
	Containerized bool
	// ScaleToZeroAfter determines how long a function must stay idle before all instances are torn down.
	// By staying idle means no new calls are being made to the function.
	ScaleToZeroAfter time.Duration
	// MaxInstancesPerWorker for the number of warm instances a single worker can host for a function.
	MaxInstancesPerWorker int
	// DialTimeout controls how long to wait when dialing a worker.
	DialTimeout time.Duration
	// StartTimeout for worker Start requests.
	StartTimeout time.Duration
	// StopTimeout for worker Stop requests.
	StopTimeout time.Duration
	// CallTimeout for worker Call requests.
	CallTimeout time.Duration
	// StatusBackoff is the retry delay when the status stream read from the workers breaks.
	StatusBackoff time.Duration
	// SchedulerFactory builds a worker scheduler per function to decide routing and scaling targets.
	SchedulerFactory WorkerSchedulerFactory
}

// applyDefaults configures the config with default values if not set.
func (c *Config) applyDefaults() {
	if c.ScaleToZeroAfter <= 0 {
		c.ScaleToZeroAfter = 10 * time.Second
	}
	if c.MaxInstancesPerWorker <= 0 {
		c.MaxInstancesPerWorker = 4
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.StartTimeout <= 0 {
		c.StartTimeout = 45 * time.Second
	}
	if c.StopTimeout <= 0 {
		c.StopTimeout = 10 * time.Second
	}
	if c.CallTimeout <= 0 {
		c.CallTimeout = 20 * time.Second
	}
	if c.StatusBackoff <= 0 {
		c.StatusBackoff = 2 * time.Second
	}
	if c.SchedulerFactory == nil {
		c.SchedulerFactory = defaultSchedulerFactory
	}
}
