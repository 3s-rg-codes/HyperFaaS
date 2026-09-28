package worker

import "sync"

// ReadySignals tracks sandbox readiness reported via SignalReady.
type ReadySignals struct {
	mu     sync.RWMutex
	states map[uint64]*readyState
}

type readyState struct {
	ready bool
	ch    chan struct{}
}

func NewReadySignals() *ReadySignals {
	return &ReadySignals{states: make(map[uint64]*readyState)}
}

func (s *ReadySignals) AddInstance(instanceID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[instanceID]
	if state == nil {
		s.states[instanceID] = &readyState{ch: make(chan struct{})}
		return
	}
	if state.ready {
		delete(s.states, instanceID)
		return
	}
	if state.ch == nil {
		state.ch = make(chan struct{})
	}
}

func (s *ReadySignals) SignalReady(instanceID uint64) {
	s.mu.Lock()
	state := s.states[instanceID]
	if state == nil {
		s.states[instanceID] = &readyState{ready: true}
		s.mu.Unlock()
		return
	}
	if state.ch != nil {
		ch := state.ch
		delete(s.states, instanceID)
		s.mu.Unlock()
		close(ch)
		return
	}
	state.ready = true
	s.mu.Unlock()
}

func (s *ReadySignals) WaitReady(instanceID uint64) {
	s.mu.RLock()
	state := s.states[instanceID]
	s.mu.RUnlock()
	if state == nil {
		return
	}
	if state.ready {
		s.mu.Lock()
		delete(s.states, instanceID)
		s.mu.Unlock()
		return
	}
	if state.ch != nil {
		<-state.ch
		s.mu.Lock()
		delete(s.states, instanceID)
		s.mu.Unlock()
	}
}
