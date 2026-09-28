package runc

import "time"

type Config struct {
	WorkDir             string
	WorkerListenAddress string
	StartTimeout        time.Duration
	NetworkIsolation    bool
	NetworkMode         string // veth, ipvlan
	UsePool             bool
	PoolSize            int
	ArtifactsBucket     string
}
