package metrics

type ResourceMetrics struct {
	// number of nanoseconds the CPU was used for the last interval.
	CPUUtilizationRaw float64
	// percentage of total system CPU used for the last interval.
	CPUUtilizationPercent float32
	// raw bytes of memory used for the last interval.
	MemoryUtilizationRaw float64
	// percentage of total system memory used for the last interval.
	MemoryUtilizationPercent float32
}
