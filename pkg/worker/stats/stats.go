package stats

import (
	"log/slog"
	"sync"
	"time"
)

type StatusUpdateQueue struct {
	Queue []StatusUpdate
	mu    sync.Mutex
}

type StatsManager struct {
	Updates        StatusUpdateQueue
	listeners      map[string]chan StatusUpdate
	toBeTerminated map[string]chan bool
	mu             sync.Mutex
	logger         *slog.Logger
}

func NewStatsManager(logger *slog.Logger) *StatsManager {
	return &StatsManager{
		Updates:        StatusUpdateQueue{Queue: make([]StatusUpdate, 0)},
		listeners:      make(map[string]chan StatusUpdate),
		toBeTerminated: make(map[string]chan bool),
		logger:         logger,
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
	s.listeners[nodeID] = listener
	s.logger.Info("Added listener with ID", "id", nodeID)
	s.mu.Unlock()
}

func (s *StatsManager) RemoveListener(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger.Debug("Removed listener", "id", nodeID)
	delete(s.listeners, nodeID)
}

func (s *StatsManager) GetListenerByID(nodeID string) chan StatusUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok1 := s.toBeTerminated[nodeID]
	updateChan, ok2 := s.listeners[nodeID]
	if ok1 && ok2 {
		s.toBeTerminated[nodeID] <- true
		delete(s.toBeTerminated, nodeID)
	} else {
		return nil
	}
	return updateChan
}

func (s *StatsManager) RemoveListenerAfter(nodeID string, sec time.Duration) {
	s.mu.Lock()
	ch := make(chan bool, 10)
	s.toBeTerminated[nodeID] = ch
	s.logger.Info("Node set to be terminated", "id", nodeID)
	s.mu.Unlock()

	select {
	case <-ch:
		break
	case <-time.After(sec * time.Second):

		s.mu.Lock()
		delete(s.listeners, nodeID)
		s.mu.Unlock()
	}
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	s.logger.Debug("Started Streaming to listeners")
	for {
		data := s.dequeue()
		if data == nil {
			continue
		}
		for nodeID, listener := range s.listeners {
			select {
			case listener <- *data:

			default:
				s.logger.Debug("Listener is full, dropping update", "node_id", nodeID)
			}
		}
	}
}
