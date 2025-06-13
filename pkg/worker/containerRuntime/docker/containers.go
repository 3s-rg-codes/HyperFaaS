package dockerRuntime

import (
	"fmt"
	"sync"
)

type RunningContainers struct {
	mu             sync.RWMutex
	containerIpMap map[string]string
}

func NewRunningContainers() *RunningContainers {
	return &RunningContainers{
		containerIpMap: make(map[string]string),
	}
}

func (c *RunningContainers) GetContainerIp(containerID string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ip, ok := c.containerIpMap[containerID]
	if !ok {
		return "", fmt.Errorf("container IP not found for container ID: %s", containerID)
	}
	return ip, nil
}

func (c *RunningContainers) SetContainerIp(containerID string, ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.containerIpMap[containerID] = ip
}

func (c *RunningContainers) DeleteContainerIp(containerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.containerIpMap, containerID)
}
