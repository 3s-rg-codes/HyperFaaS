package controller

import "sync"

type ReadySignals struct {
	readySignals map[string]chan struct{}
	mu           sync.RWMutex
}

func NewReadySignals() *ReadySignals {
	return &ReadySignals{readySignals: make(map[string]chan struct{})}
}

func (s *ReadySignals) AddInstance(instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readySignals[instanceID] = make(chan struct{})
}

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

func (s *ReadySignals) WaitReady(instanceID string) {
	s.mu.RLock()
	c := s.readySignals[instanceID]
	s.mu.RUnlock()
	if c != nil {
		<-c
	}
}
