package stats

import (
	"sync"

	"github.com/rs/zerolog/log"
)

type StatusUpdateQueue struct {
	Queue []StatusUpdate
	mu    sync.Mutex
}

type StatsManager struct {
	Updates   StatusUpdateQueue
	listeners map[string]chan StatusUpdate
	mu        sync.Mutex
}

func New() StatsManager {
	return StatsManager{
		Updates:   StatusUpdateQueue{Queue: make([]StatusUpdate, 0)},
		listeners: make(map[string]chan StatusUpdate),
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

	log.Debug().Msgf("Adding listener for node %s", nodeID)
	s.mu.Lock()
	s.listeners[nodeID] = listener
	s.mu.Unlock()
}

func (s *StatsManager) RemoveListener(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, nodeID)
	log.Debug().Msgf("Removed listener for node %s", nodeID)
}

func (s *StatsManager) GetListenerByID(nodeID string) chan StatusUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.listeners[nodeID]
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	for {
		data := s.dequeue()
		if data == nil {
			continue
		}
		for nodeID, listener := range s.listeners {
			select {
			case listener <- *data:
				log.Debug().Msgf("Buffered update to node %s", nodeID)
			default:
				log.Debug().Msgf("Listener for node %s is full, dropping update", nodeID)

			}
		}
	}
}
