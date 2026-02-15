// Package cache provides a generic, thread-safe LRU cache with TTL support,
// stale-while-revalidate, and tag-based invalidation.
package cache

import (
	"container/list"
	"sync"
	"time"
)

// entry is a cache entry stored in the doubly-linked list.
type entry[K comparable, V any] struct {
	key       K
	value     V
	tags      []string
	expiresAt time.Time
	staleAt   time.Time // After this time, entry is "stale" but still serveable during SWR
}

// OnEvictFunc is called when an entry is evicted from the cache.
type OnEvictFunc[K comparable, V any] func(key K, value V)

// FetchFunc is used for stale-while-revalidate: called in background to refresh a stale entry.
type FetchFunc[K comparable, V any] func(key K) (V, error)

// Options configures the LRU cache.
type Options[K comparable, V any] struct {
	// MaxSize is the maximum number of entries. Zero means unlimited.
	MaxSize int

	// TTL is the default time-to-live for entries. Zero means no expiration.
	TTL time.Duration

	// StaleTTL is the additional duration after TTL during which stale entries
	// are still returned while a background refresh runs. Zero disables SWR.
	StaleTTL time.Duration

	// OnEvict is called when an entry is evicted (optional).
	OnEvict OnEvictFunc[K, V]

	// FetchFunc is called in the background to refresh stale entries (optional, enables SWR).
	FetchFunc FetchFunc[K, V]

	// Now returns the current time. Defaults to time.Now if nil (useful for testing).
	Now func() time.Time
}

// LRU is a generic, thread-safe least-recently-used cache with TTL support.
type LRU[K comparable, V any] struct {
	mu        sync.Mutex
	items     map[K]*list.Element
	evictList *list.List
	tags      map[string]map[K]struct{} // tag -> set of keys
	opts      Options[K, V]
	now       func() time.Time
}

// New creates a new LRU cache with the given options.
func New[K comparable, V any](opts Options[K, V]) *LRU[K, V] {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &LRU[K, V]{
		items:     make(map[K]*list.Element),
		evictList: list.New(),
		tags:      make(map[string]map[K]struct{}),
		opts:      opts,
		now:       nowFn,
	}
}

// Set adds or updates an entry with the default TTL.
func (c *LRU[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, c.opts.TTL)
}

// SetWithTags adds or updates an entry with the default TTL and associated tags.
func (c *LRU[K, V]) SetWithTags(key K, value V, tags []string) {
	c.SetWithOptions(key, value, c.opts.TTL, tags)
}

// SetWithTTL adds or updates an entry with a specific TTL.
func (c *LRU[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	c.SetWithOptions(key, value, ttl, nil)
}

// SetWithOptions adds or updates an entry with a specific TTL and tags.
func (c *LRU[K, V]) SetWithOptions(key K, value V, ttl time.Duration, tags []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	var expiresAt, staleAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
		if c.opts.StaleTTL > 0 {
			staleAt = expiresAt.Add(c.opts.StaleTTL)
		}
	}

	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		ent := elem.Value.(*entry[K, V])
		// Remove old tags
		c.removeTagsLocked(key, ent.tags)
		ent.value = value
		ent.expiresAt = expiresAt
		ent.staleAt = staleAt
		ent.tags = tags
		c.addTagsLocked(key, tags)
		return
	}

	ent := &entry[K, V]{
		key:       key,
		value:     value,
		tags:      tags,
		expiresAt: expiresAt,
		staleAt:   staleAt,
	}
	elem := c.evictList.PushFront(ent)
	c.items[key] = elem
	c.addTagsLocked(key, tags)

	if c.opts.MaxSize > 0 && c.evictList.Len() > c.opts.MaxSize {
		c.evictOldestLocked()
	}
}

// Get retrieves an entry. Returns the value and true if found and not expired.
// If SWR is enabled and the entry is stale, returns the stale value (true) and
// triggers a background refresh.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}

	ent := elem.Value.(*entry[K, V])
	now := c.now()

	// Check hard expiration (past stale window or no SWR)
	if !ent.expiresAt.IsZero() {
		hardDeadline := ent.expiresAt
		if !ent.staleAt.IsZero() {
			hardDeadline = ent.staleAt
		}
		if now.After(hardDeadline) {
			c.removeLocked(key)
			var zero V
			return zero, false
		}
	}

	// Check if stale (past TTL but within SWR window)
	if !ent.expiresAt.IsZero() && now.After(ent.expiresAt) && !ent.staleAt.IsZero() {
		// Entry is stale — return it but trigger background refresh
		c.evictList.MoveToFront(elem)
		if c.opts.FetchFunc != nil {
			go c.refreshEntry(key)
		}
		return ent.value, true
	}

	c.evictList.MoveToFront(elem)
	return ent.value, true
}

// Peek retrieves an entry without updating its position in the LRU list.
func (c *LRU[K, V]) Peek(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}

	ent := elem.Value.(*entry[K, V])
	now := c.now()

	if !ent.expiresAt.IsZero() {
		hardDeadline := ent.expiresAt
		if !ent.staleAt.IsZero() {
			hardDeadline = ent.staleAt
		}
		if now.After(hardDeadline) {
			c.removeLocked(key)
			var zero V
			return zero, false
		}
	}

	return ent.value, true
}

// Delete removes an entry from the cache.
func (c *LRU[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}

// InvalidateByTag removes all entries associated with the given tag.
func (c *LRU[K, V]) InvalidateByTag(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys, ok := c.tags[tag]
	if !ok {
		return
	}
	// Collect keys first to avoid modifying the map during iteration
	toRemove := make([]K, 0, len(keys))
	for k := range keys {
		toRemove = append(toRemove, k)
	}
	for _, k := range toRemove {
		c.removeLocked(k)
	}
}

// Len returns the number of entries in the cache.
func (c *LRU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// Clear removes all entries from the cache.
func (c *LRU[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.opts.OnEvict != nil {
		for _, elem := range c.items {
			ent := elem.Value.(*entry[K, V])
			c.opts.OnEvict(ent.key, ent.value)
		}
	}

	c.items = make(map[K]*list.Element)
	c.evictList.Init()
	c.tags = make(map[string]map[K]struct{})
}

// Keys returns all keys in the cache, ordered from most to least recently used.
func (c *LRU[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]K, 0, c.evictList.Len())
	for elem := c.evictList.Front(); elem != nil; elem = elem.Next() {
		ent := elem.Value.(*entry[K, V])
		keys = append(keys, ent.key)
	}
	return keys
}

// Purge removes all expired entries from the cache.
func (c *LRU[K, V]) Purge() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	purged := 0
	for elem := c.evictList.Back(); elem != nil; {
		prev := elem.Prev()
		ent := elem.Value.(*entry[K, V])
		if !ent.expiresAt.IsZero() {
			deadline := ent.expiresAt
			if !ent.staleAt.IsZero() {
				deadline = ent.staleAt
			}
			if now.After(deadline) {
				c.removeLocked(ent.key)
				purged++
			}
		}
		elem = prev
	}
	return purged
}

// --- internal helpers ---

func (c *LRU[K, V]) removeLocked(key K) {
	elem, ok := c.items[key]
	if !ok {
		return
	}
	ent := elem.Value.(*entry[K, V])
	c.removeTagsLocked(key, ent.tags)
	c.evictList.Remove(elem)
	delete(c.items, key)

	if c.opts.OnEvict != nil {
		c.opts.OnEvict(ent.key, ent.value)
	}
}

func (c *LRU[K, V]) evictOldestLocked() {
	elem := c.evictList.Back()
	if elem == nil {
		return
	}
	ent := elem.Value.(*entry[K, V])
	c.removeLocked(ent.key)
}

func (c *LRU[K, V]) addTagsLocked(key K, tags []string) {
	for _, tag := range tags {
		if c.tags[tag] == nil {
			c.tags[tag] = make(map[K]struct{})
		}
		c.tags[tag][key] = struct{}{}
	}
}

func (c *LRU[K, V]) removeTagsLocked(key K, tags []string) {
	for _, tag := range tags {
		if tagSet, ok := c.tags[tag]; ok {
			delete(tagSet, key)
			if len(tagSet) == 0 {
				delete(c.tags, tag)
			}
		}
	}
}

func (c *LRU[K, V]) refreshEntry(key K) {
	if c.opts.FetchFunc == nil {
		return
	}
	value, err := c.opts.FetchFunc(key)
	if err != nil {
		return // Keep stale entry on refresh failure
	}
	// Re-insert with fresh TTL
	c.Set(key, value)
}
