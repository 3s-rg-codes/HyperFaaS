package config

import "time"

type LeafConfig struct {
	MaxStartingInstancesPerFunction int
	StartingInstanceWaitTimeout     time.Duration
	MaxRunningInstancesPerFunction  int
	PanicBackoff                    time.Duration
	PanicBackoffIncrease            time.Duration
	PanicMaxBackoff                 time.Duration
}
