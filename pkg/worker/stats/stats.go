package stats

import (
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

type StatsManager struct {
	Updates          chan StatusUpdate
	UpdateBufferSize int64
	sampleRate       float64
	listeners        map[string]chan StatusUpdate
	toBeTerminated   map[string]chan bool
	mu               sync.RWMutex
	logger           *slog.Logger
	listenerTimeout  time.Duration
}

func NewStatsManager(logger *slog.Logger, listenerTimeout time.Duration, sampleRate float64, updateBufferSize int64) *StatsManager {
	return &StatsManager{
		Updates:          make(chan StatusUpdate, updateBufferSize),
		UpdateBufferSize: updateBufferSize,
		listeners:        make(map[string]chan StatusUpdate),
		toBeTerminated:   make(map[string]chan bool),
		logger:           logger,
		listenerTimeout:  listenerTimeout,
		sampleRate:       sampleRate,
	}
}

func (s *StatsManager) Enqueue(su *StatusUpdate) {

	// If its a timeout or a crash, we must always send the update
	if su.Event == EventTimeout || su.Event == EventDown {
		select {
		case s.Updates <- *su:
			return
		default:
			s.logger.Warn("Updates channel is full, dropping update")
			return
		}
	}
	// For all other cases, we only send the update if the random number is less than the sample rate
	if rand.Float64() < s.sampleRate {
		select {
		case s.Updates <- *su:
		default:
			s.logger.Warn("Updates channel is full, dropping update")
		}
	}
	/* go func() {
		s.Updates <- *su
	}() */
}

func (s *StatsManager) AddListener(nodeID string, listener chan StatusUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up any existing listener for this node
	if _, exists := s.listeners[nodeID]; exists {
		// Don't close the channel here as it might be in use elsewhere
		delete(s.listeners, nodeID)
	}

	s.listeners[nodeID] = listener
	s.logger.Info("Added listener with ID", "id", nodeID)
}

func (s *StatsManager) RemoveListener(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from termination map if present
	if _, exists := s.toBeTerminated[nodeID]; exists {
		delete(s.toBeTerminated, nodeID)
		// Don't close the channel here as it might be in use elsewhere
	}

	// Remove from listeners
	delete(s.listeners, nodeID)
	s.logger.Debug("Removed listener", "id", nodeID)
}

func (s *StatsManager) GetListenerByID(nodeID string) chan StatusUpdate {
	s.mu.Lock()

	// Check if the node exists
	terminationCh, hasTermination := s.toBeTerminated[nodeID]
	updateChan, hasListener := s.listeners[nodeID]

	if !hasListener {
		s.mu.Unlock()
		return nil
	}

	// If it's marked for termination, cancel that
	if hasTermination {
		delete(s.toBeTerminated, nodeID)
		s.mu.Unlock()

		// Signal after releasing the lock
		select {
		case terminationCh <- true:
			// Successfully signaled
		default:
			s.logger.Warn("Failed to signal termination channel", "node_id", nodeID)
		}
	} else {
		s.mu.Unlock()
	}

	return updateChan
}

func (s *StatsManager) RemoveListenerAfterTimeout(nodeID string) {
	terminationCh := make(chan bool, 1)

	s.mu.Lock()
	// Clean up any existing termination channel
	if _, exists := s.toBeTerminated[nodeID]; exists {
		delete(s.toBeTerminated, nodeID)
		// Don't close here as it might be in use
	}

	s.toBeTerminated[nodeID] = terminationCh
	s.logger.Info("Node set to be terminated", "id", nodeID)
	s.mu.Unlock()

	// Wait for either signal or timeout
	select {
	case <-terminationCh:
		// Termination cancelled, clean up channel
		s.logger.Debug("Termination cancelled for node", "id", nodeID)
	case <-time.After(s.listenerTimeout):
		// Timeout occurred, remove the listener
		s.mu.Lock()
		delete(s.listeners, nodeID)
		delete(s.toBeTerminated, nodeID)
		s.mu.Unlock()
		s.logger.Debug("Listener removed after timeout", "id", nodeID)
	}

	// Close the channel when done with it
	close(terminationCh)
}

// Streams the status updates to all channels in the listeners map.
func (s *StatsManager) StartStreamingToListeners() {
	for update := range s.Updates {
		// Make a copy of the listeners map to avoid holding the lock during sends
		s.mu.RLock()
		activeListeners := make(map[string]chan StatusUpdate, len(s.listeners))
		for nodeID, listener := range s.listeners {
			activeListeners[nodeID] = listener
		}
		s.mu.RUnlock()

		// Send updates to all active listeners
		for nodeID, listener := range activeListeners {
			select {
			case listener <- update:
				// Successfully sent
			default:
				s.logger.Debug("Listener is full, dropping update", "node_id", nodeID)
			}
		}
	}
}
