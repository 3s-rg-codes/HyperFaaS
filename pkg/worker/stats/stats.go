package stats

import (
	"log/slog"
	"sync"
)

type StatusUpdateQueue struct {
	Queue []StatusUpdate
	mu    sync.Mutex
}

type StatsManager struct {
	Updates   StatusUpdateQueue
	listeners map[string]chan StatusUpdate
	mu        sync.Mutex
	logger    *slog.Logger
}

func NewStatsManager(logger *slog.Logger) *StatsManager {
	return &StatsManager{
		Updates:   StatusUpdateQueue{Queue: make([]StatusUpdate, 0)},
		listeners: make(map[string]chan StatusUpdate),
		logger:    logger,
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
	s.logger.Info("Adding listener", "node_id", nodeID)
	s.mu.Lock()
	s.listeners[nodeID] = listener
	s.mu.Unlock()
}

func (s *StatsManager) RemoveListener(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.listeners, nodeID)
}

func (s *StatsManager) GetListenerByID(nodeID string) chan StatusUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.listeners[nodeID]
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	s.logger.Info("Started Streaming to listeners")
	for {
		data := s.dequeue()
		if data == nil {
			continue
		}
		for nodeID, listener := range s.listeners {
			select {
			case listener <- *data:
				s.logger.Debug("Buffered update", "node_id", nodeID) //TODO: This doesnt work since the channel will just block until there is space again
			default:
				s.logger.Debug("Listener is full, dropping update", "node_id", nodeID)

			}
		}
	}
}
