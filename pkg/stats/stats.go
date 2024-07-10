package stats

import (
	"sync"
)

type StatusUpdateQueue struct {
	Queue []StatusUpdate
	mu    sync.Mutex
}

type StatsManager struct {
	Updates   StatusUpdateQueue
	listeners map[string]chan StatusUpdate
	history   map[string][]StatusUpdate
	mu        sync.Mutex
}

func New() StatsManager {
	return StatsManager{
		Updates:   StatusUpdateQueue{Queue: make([]StatusUpdate, 0)},
		listeners: make(map[string]chan StatusUpdate),
		history:   make(map[string][]StatusUpdate),
	}
}

func (s *StatsManager) Enqueue(su *StatusUpdate) {
	s.Updates.mu.Lock()
	defer s.Updates.mu.Unlock()

	s.Updates.Queue = append(s.Updates.Queue, *su)
}

func (s *StatsManager) dequeue() *StatusUpdate {
	s.Updates.mu.Lock()
	defer s.Updates.mu.Unlock()

	if len(s.Updates.Queue) == 0 {
		return nil
	}
	data := s.Updates.Queue[0]
	s.Updates.Queue = s.Updates.Queue[1:]
	return &data
}

func (s *StatsManager) AddListener(nodeID string, listener chan StatusUpdate) {
	s.mu.Lock()
	//defer s.mu.Unlock()

	s.listeners[nodeID] = listener
	s.mu.Unlock()

	// If there are any historical updates, send them first
	if history, ok := s.history[nodeID]; ok {
		go func() {
			for _, update := range history {
				listener <- update
			}
			// Clear history after sending
			delete(s.history, nodeID)
		}()
	}
}

func (s *StatsManager) RemoveListener(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, nodeID)
}

func (s *StatsManager) StoreHistory(nodeID string, updates []StatusUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.history[nodeID]; !ok {
		s.history[nodeID] = make([]StatusUpdate, 0)
	}
	s.history[nodeID] = append(s.history[nodeID], updates...)
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	for {
		data := s.dequeue()
		if data == nil {
			continue
		}
		s.mu.Lock()
		for nodeID, listener := range s.listeners {
			select {
			case listener <- *data:
			default:
				// If the listener is not ready to receive, store the update
				s.StoreHistory(nodeID, []StatusUpdate{*data})
			}
		}
		s.mu.Unlock()
	}
}
