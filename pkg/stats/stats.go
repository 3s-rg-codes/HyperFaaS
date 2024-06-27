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
}

func New() StatsManager {
	return StatsManager{

		Updates: StatusUpdateQueue{Queue: make([]StatusUpdate, 0)},

		listeners: make(map[string]chan StatusUpdate),
	}
}

func (s *StatsManager) Enqueue(su *StatusUpdate) {
	s.Updates.mu.Lock()
	defer s.Updates.mu.Unlock()

	s.Updates.Queue = append(s.Updates.Queue, *su)
}

func (s *StatsManager) dequeue() *StatusUpdate {
	if len(s.Updates.Queue) == 0 {
		return nil
	}
	data := s.Updates.Queue[0]
	s.Updates.Queue = s.Updates.Queue[1:]
	return &data
}

func (s *StatsManager) AddListener(nodeID string, listener chan StatusUpdate) {
	s.listeners[nodeID] = listener
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	for {
		data := s.dequeue()
		if data == nil {
			continue
		}
		for _, listener := range s.listeners {
			listener <- *data
		}
	}
}
