package controller

import "sync"

// ReadySignals handles receiving ready signals from instances and keeps this state in memory.
// This is used in the controller to block until an instance is ready before returning in the Start function.
// This ensures that clients can start calling the instance immediately after starting it.
type ReadySignals struct {
	readySignals map[string]*readyState
	mu           sync.RWMutex
	// wether the readySignals are mocked. Used for testing.
	mocked bool
}

type readyState struct {
	ready bool
	ch    chan struct{}
}

// NewReadySignals creates a new ReadySignals instance. Use mocked: true to use the mocked runtime.
func NewReadySignals(mocked bool) *ReadySignals {
	return &ReadySignals{
		readySignals: make(map[string]*readyState),
		mocked:       mocked,
	}
}

// AddInstance adds a new instance to the readySignals map.
func (s *ReadySignals) AddInstance(instanceID string) {
	if s.mocked {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readySignals[instanceID]
	if state == nil {
		s.readySignals[instanceID] = &readyState{ch: make(chan struct{})}
		return
	}
	if state.ready {
		delete(s.readySignals, instanceID)
		return
	}
	if state.ch == nil {
		state.ch = make(chan struct{})
	}
}

// SignalReady signals that the instance is ready to serve requests.
func (s *ReadySignals) SignalReady(instanceID string) {
	if s.mocked {
		return
	}
	s.mu.Lock()
	state := s.readySignals[instanceID]
	if state == nil {
		s.readySignals[instanceID] = &readyState{ready: true}
		s.mu.Unlock()
		return
	}
	if state.ch != nil {
		ch := state.ch
		delete(s.readySignals, instanceID)
		s.mu.Unlock()
		close(ch)
		return
	}
	state.ready = true
	s.mu.Unlock()
}

// WaitReady blocks until the instance is ready to serve requests.
func (s *ReadySignals) WaitReady(instanceID string) {
	if s.mocked {
		return
	}
	s.mu.RLock()
	state := s.readySignals[instanceID]
	s.mu.RUnlock()
	if state == nil {
		return
	}
	if state.ready {
		s.mu.Lock()
		delete(s.readySignals, instanceID)
		s.mu.Unlock()
		return
	}
	if state.ch != nil {
		<-state.ch
		s.mu.Lock()
		delete(s.readySignals, instanceID)
		s.mu.Unlock()
	}
}
