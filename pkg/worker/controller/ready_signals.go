package controller

import "sync"

// ReadySignals handles receiving ready signals from instances and keeps this state in memory.
// This is used in the controller to block until an instance is ready before returning in the Start function.
// This ensures that clients can start calling the instance immediately after starting it.
type ReadySignals struct {
	readySignals map[string]chan struct{}
	mu           sync.RWMutex
}

func NewReadySignals() *ReadySignals {
	return &ReadySignals{readySignals: make(map[string]chan struct{})}
}

// AddInstance adds a new instance to the readySignals map.
func (s *ReadySignals) AddInstance(instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readySignals[instanceID] = make(chan struct{})
}

// SignalReady signals that the instance is ready to serve requests.
func (s *ReadySignals) SignalReady(instanceID string) {
	s.mu.Lock()
	c := s.readySignals[instanceID]
	s.mu.Unlock()
	if c != nil {
		c <- struct{}{}
		s.mu.Lock()
		close(c)
		delete(s.readySignals, instanceID)
		s.mu.Unlock()
	}
}

// WaitReady blocks until the instance is ready to serve requests.
func (s *ReadySignals) WaitReady(instanceID string) {
	s.mu.RLock()
	c := s.readySignals[instanceID]
	s.mu.RUnlock()
	if c != nil {
		<-c
	}
}
