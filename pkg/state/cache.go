package state

import (
	"slices"
	"sync"
)

// ChildCache is a thread safe cache to store our child state in.
// The main idea for the mapping is function_id -> list of downstream nodes (workers, leaves, etc.).
// For now there is no raking or order in the children for a given key. In the future, we could have an optimised structure that lets us make reads faster (for example, if the first element is always the best child to pick).
// This is not trivial as there are many factors to consider. A child might have more warm capacity, or more resources available, but this can quickly change with the metrics received.
type ChildCache[K comparable, V comparable] struct {
	mu   sync.RWMutex
	data map[K][]V
}

func NewCache[K comparable, V comparable]() *ChildCache[K, V] {
	return &ChildCache[K, V]{data: make(map[K][]V)}
}

// Appends a value to the list of children for a given key.
func (c *ChildCache[K, V]) Append(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = append(c.data[key], value)
}

// Sets the list of children for a given key to the given value.

func (c *ChildCache[K, V]) Set(key K, value []V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
}

// Get returns the list of children for a given key and a boolean indicating if the key exists.
func (c *ChildCache[K, V]) Get(key K) ([]V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data[key], true
}

// Deletes the list of children for a given key.
func (c *ChildCache[K, V]) DeleteKey(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
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

var LBCache = NewCache[string, string]()
