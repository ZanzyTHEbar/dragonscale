package agent

import "sync"

// boundedCache is a concurrent-safe, bounded key-value cache that evicts
// the oldest half of entries when capacity is reached. Sufficient for
// caching session→conversationID mappings where exact LRU ordering is
// not critical but unbounded growth must be prevented.
type boundedCache[K comparable, V any] struct {
	mu       sync.RWMutex
	items    map[K]V
	order    []K
	capacity int
}

func newBoundedCache[K comparable, V any](capacity int) *boundedCache[K, V] {
	if capacity <= 0 {
		capacity = 1024
	}
	return &boundedCache[K, V]{
		items:    make(map[K]V, capacity),
		order:    make([]K, 0, capacity),
		capacity: capacity,
	}
}

func (c *boundedCache[K, V]) Load(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[key]
	return v, ok
}

func (c *boundedCache[K, V]) Store(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists {
		if len(c.items) >= c.capacity {
			c.evictHalfLocked()
		}
		c.order = append(c.order, key)
	}
	c.items[key] = value
}

func (c *boundedCache[K, V]) evictHalfLocked() {
	evictCount := len(c.order) / 2
	if evictCount == 0 {
		evictCount = 1
	}
	for _, k := range c.order[:evictCount] {
		delete(c.items, k)
	}
	c.order = append(c.order[:0], c.order[evictCount:]...)
}

func (c *boundedCache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
