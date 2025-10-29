package state

import (
	"slices"
	"sync"
)

// ChildCache is a thread safe cache to store our child state in.
// It can also send updates about wether a child id (K) has at least one V (child) registered.
// The main use case for this is notifying parent routing controllers about downstream changes, for example, if the last available instance of a function is deleted.
// The main idea for the mapping is function_id -> list of downstream nodes (workers, leaves, etc.).
// For now there is no raking or order in the children for a given key. In the future, we could have an optimised structure that lets us make reads faster (for example, if the first element is always the best child to pick).
// This is not trivial as there are many factors to consider. A child might have more warm capacity, or more resources available, but this can quickly change with the metrics received.
type ChildCache[K comparable, V comparable] struct {
	mu   sync.RWMutex
	data map[K][]V
	// whether to send updates when: a K is deleted or its len([]V) ==0
	// in which case have = false
	// or a new value is added and len([]V) > 0
	// in which case have = true
	sendUpdates bool

	// channel to send updates about K's state changes
	updates chan Update[K]
}

type Update[K comparable] struct {
	Id   K
	Have bool
}

func NewCache[K comparable, V comparable](sendUpdates bool) *ChildCache[K, V] {
	return &ChildCache[K, V]{
		data:        make(map[K][]V),
		sendUpdates: sendUpdates,
		updates:     make(chan Update[K]),
	}
}

// Appends a value to the list of children for a given key.
func (c *ChildCache[K, V]) Append(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.data[key]
	wasLen := len(list)
	if !slices.Contains(list, value) {
		c.data[key] = append(list, value)
	}
	if wasLen == 0 {
		// This is the first downstream node for this key
		// so we send an update that the key has at least one downstream node.
		c.SendUpdate(key, true)
	}
}

// Sets the list of children for a given key to the given value.
func (c *ChildCache[K, V]) Set(key K, value []V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := make([]V, len(value))
	copy(clone, value)
	c.data[key] = clone

	// send update about the new state of the key.
	c.SendUpdate(key, len(clone) > 0)
}

// Get returns a copy of the list of children for a given key and a boolean indicating if the key exists.
func (c *ChildCache[K, V]) Get(key K) ([]V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	list, ok := c.data[key]
	if !ok {
		return nil, false
	}
	clone := make([]V, len(list))
	copy(clone, list)
	return clone, true
}

// Deletes the list of children for a given key.
func (c *ChildCache[K, V]) DeleteKey(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	c.SendUpdate(key, false)
}

// Remove a value from the list of children for a given key.
func (c *ChildCache[K, V]) Remove(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list, ok := c.data[key]
	if !ok {
		return
	}
	newList := slices.DeleteFunc(list, func(v V) bool {
		return v == value
	})
	c.data[key] = newList

	if len(newList) == 0 {
		c.SendUpdate(key, false)
	}
}

// Keys returns a copy of the list of keys in the cache.
func (c *ChildCache[K, V]) Keys() []K {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]K, 0, len(c.data))
	for key := range c.data {
		keys = append(keys, key)
	}
	return keys
}

// SendUpdate sends an update about the state of a K to the updates channel asynchronously.
// TODO: this is a potential memory leak if the updates channel is not read from.
// In theory, if that happens, it means that the parent is bugged / down, which already
// means a terrible situation, so it should never happen???
func (c *ChildCache[K, V]) SendUpdate(id K, have bool) {
	if !c.sendUpdates {
		return
	}
	go func() {
		c.updates <- Update[K]{Id: id, Have: have}
	}()
}

// Updates returns a channel to receive updates about the state of the children.
func (c *ChildCache[K, V]) Updates() <-chan Update[K] {
	return c.updates
}
