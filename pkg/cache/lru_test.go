package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockClock provides a controllable clock for testing.
type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{now: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// --- Basic LRU Tests ---

func TestLRU_SetAndGet(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 10})

	c.Set("a", 1)
	c.Set("b", 2)

	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}

	v, ok = c.Get("b")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", v, ok)
	}

	_, ok = c.Get("missing")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestLRU_Update(t *testing.T) {
	t.Parallel()
	c := New[string, string](Options[string, string]{MaxSize: 10})

	c.Set("k", "v1")
	c.Set("k", "v2")

	v, ok := c.Get("k")
	if !ok || v != "v2" {
		t.Fatalf("expected (v2, true), got (%s, %v)", v, ok)
	}

	if c.Len() != 1 {
		t.Fatalf("expected len 1, got %d", c.Len())
	}
}

func TestLRU_EvictionOrder(t *testing.T) {
	t.Parallel()
	var evicted []string
	c := New[string, int](Options[string, int]{
		MaxSize: 3,
		OnEvict: func(key string, _ int) {
			evicted = append(evicted, key)
		},
	})

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Cache is full: [c, b, a] (front to back)

	// Access "a" to move it to front: [a, c, b]
	c.Get("a")

	// Add "d" — should evict "b" (LRU)
	c.Set("d", 4)

	if len(evicted) != 1 || evicted[0] != "b" {
		t.Fatalf("expected eviction of 'b', got %v", evicted)
	}

	_, ok := c.Get("b")
	if ok {
		t.Fatal("expected 'b' to be evicted")
	}

	// Remaining: [d, a, c]
	keys := c.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "d" || keys[1] != "a" || keys[2] != "c" {
		t.Fatalf("expected [d, a, c], got %v", keys)
	}
}

func TestLRU_MaxSizeZero_Unlimited(t *testing.T) {
	t.Parallel()
	c := New[int, int](Options[int, int]{})

	for i := 0; i < 1000; i++ {
		c.Set(i, i)
	}

	if c.Len() != 1000 {
		t.Fatalf("expected 1000, got %d", c.Len())
	}
}

func TestLRU_Delete(t *testing.T) {
	t.Parallel()
	var evictCalled bool
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		OnEvict: func(key string, _ int) {
			evictCalled = true
		},
	})

	c.Set("a", 1)
	c.Delete("a")

	if !evictCalled {
		t.Fatal("expected OnEvict to be called on Delete")
	}

	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected 'a' to be deleted")
	}

	if c.Len() != 0 {
		t.Fatalf("expected len 0, got %d", c.Len())
	}
}

func TestLRU_Clear(t *testing.T) {
	t.Parallel()
	var evictCount int
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		OnEvict: func(_ string, _ int) {
			evictCount++
		},
	})

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Clear()

	if evictCount != 3 {
		t.Fatalf("expected 3 evictions, got %d", evictCount)
	}

	if c.Len() != 0 {
		t.Fatalf("expected len 0, got %d", c.Len())
	}
}

func TestLRU_Peek_DoesNotPromote(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 3})

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Order: [c, b, a]

	// Peek "a" — should NOT move it to front
	v, ok := c.Peek("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}

	// Add "d" — should evict "a" (still LRU since Peek didn't promote)
	c.Set("d", 4)

	_, ok = c.Get("a")
	if ok {
		t.Fatal("expected 'a' to be evicted (Peek should not promote)")
	}
}

// --- TTL Tests ---

func TestLRU_TTL_Expiry(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		TTL:     5 * time.Minute,
		Now:     clock.Now,
	})

	c.Set("a", 1)

	// Before expiry
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true) before TTL, got (%d, %v)", v, ok)
	}

	// Advance past TTL
	clock.Advance(6 * time.Minute)

	_, ok = c.Get("a")
	if ok {
		t.Fatal("expected 'a' to be expired after TTL")
	}

	if c.Len() != 0 {
		t.Fatalf("expected len 0 after expiry, got %d", c.Len())
	}
}

func TestLRU_TTL_PerEntry(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		TTL:     10 * time.Minute,
		Now:     clock.Now,
	})

	c.Set("long", 1)
	c.SetWithTTL("short", 2, 2*time.Minute)

	clock.Advance(3 * time.Minute)

	// "short" should be expired
	_, ok := c.Get("short")
	if ok {
		t.Fatal("expected 'short' to be expired")
	}

	// "long" should still be alive
	v, ok := c.Get("long")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
}

func TestLRU_Peek_ExpiresEntries(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		TTL:     1 * time.Minute,
		Now:     clock.Now,
	})

	c.Set("a", 1)
	clock.Advance(2 * time.Minute)

	_, ok := c.Peek("a")
	if ok {
		t.Fatal("expected Peek to detect expired entry")
	}
}

func TestLRU_Purge(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		TTL:     5 * time.Minute,
		Now:     clock.Now,
	})

	c.Set("a", 1)
	c.Set("b", 2)
	c.SetWithTTL("c", 3, 1*time.Minute)

	clock.Advance(2 * time.Minute)

	purged := c.Purge()
	if purged != 1 {
		t.Fatalf("expected 1 purged, got %d", purged)
	}

	if c.Len() != 2 {
		t.Fatalf("expected 2 remaining, got %d", c.Len())
	}

	// "c" should be gone, "a" and "b" alive
	_, ok := c.Peek("c")
	if ok {
		t.Fatal("expected 'c' to be purged")
	}
}

// --- Stale-While-Revalidate Tests ---

func TestLRU_SWR_ReturnsStaleAndRefreshes(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	var fetchCalls atomic.Int32
	refreshDone := make(chan struct{}, 1)

	c := New[string, string](Options[string, string]{
		MaxSize:  10,
		TTL:      5 * time.Minute,
		StaleTTL: 10 * time.Minute,
		Now:      clock.Now,
		FetchFunc: func(key string) (string, error) {
			fetchCalls.Add(1)
			refreshDone <- struct{}{}
			return "refreshed-" + key, nil
		},
	})

	c.Set("k", "original")

	// Advance past TTL but within SWR window
	clock.Advance(7 * time.Minute)

	// Should return stale value
	v, ok := c.Get("k")
	if !ok || v != "original" {
		t.Fatalf("expected stale (original, true), got (%s, %v)", v, ok)
	}

	// Wait for background refresh
	<-refreshDone

	// Small sleep to let the goroutine finish Set()
	time.Sleep(50 * time.Millisecond)

	if fetchCalls.Load() != 1 {
		t.Fatalf("expected 1 fetch call, got %d", fetchCalls.Load())
	}

	// Now entry should be refreshed
	// Reset clock to make the refreshed entry fresh
	clock.Advance(0) // keep same time
	v, ok = c.Get("k")
	if !ok || v != "refreshed-k" {
		t.Fatalf("expected (refreshed-k, true), got (%s, %v)", v, ok)
	}
}

func TestLRU_SWR_HardExpiry(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize:  10,
		TTL:      5 * time.Minute,
		StaleTTL: 10 * time.Minute,
		Now:      clock.Now,
	})

	c.Set("k", 42)

	// Advance past both TTL and StaleTTL (5+10 = 15 min window)
	clock.Advance(16 * time.Minute)

	_, ok := c.Get("k")
	if ok {
		t.Fatal("expected hard expiry past SWR window")
	}
}

func TestLRU_SWR_NoFetchFunc_StaleStillReturned(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize:  10,
		TTL:      5 * time.Minute,
		StaleTTL: 10 * time.Minute,
		Now:      clock.Now,
		// No FetchFunc — SWR still returns stale, just no background refresh
	})

	c.Set("k", 99)
	clock.Advance(7 * time.Minute) // past TTL, within SWR

	v, ok := c.Get("k")
	if !ok || v != 99 {
		t.Fatalf("expected stale (99, true) without FetchFunc, got (%d, %v)", v, ok)
	}
}

// --- Tag-Based Invalidation Tests ---

func TestLRU_Tags_InvalidateByTag(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 10})

	c.SetWithTags("user:1", 1, []string{"users"})
	c.SetWithTags("user:2", 2, []string{"users"})
	c.SetWithTags("post:1", 10, []string{"posts"})
	c.SetWithTags("user:1:posts", 5, []string{"users", "posts"})

	if c.Len() != 4 {
		t.Fatalf("expected 4 entries, got %d", c.Len())
	}

	c.InvalidateByTag("users")

	if c.Len() != 1 {
		t.Fatalf("expected 1 entry after invalidating 'users', got %d", c.Len())
	}

	// "post:1" should survive
	v, ok := c.Get("post:1")
	if !ok || v != 10 {
		t.Fatalf("expected (10, true), got (%d, %v)", v, ok)
	}

	// "user:*" should be gone
	_, ok = c.Get("user:1")
	if ok {
		t.Fatal("expected 'user:1' invalidated")
	}
	_, ok = c.Get("user:1:posts")
	if ok {
		t.Fatal("expected 'user:1:posts' invalidated")
	}
}

func TestLRU_Tags_InvalidateNonexistentTag(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 10})
	c.Set("a", 1)

	// Should not panic
	c.InvalidateByTag("nonexistent")

	if c.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", c.Len())
	}
}

func TestLRU_Tags_UpdateRemovesOldTags(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 10})

	c.SetWithTags("k", 1, []string{"tag-a"})
	c.SetWithTags("k", 2, []string{"tag-b"})

	// Invalidating old tag should not remove the entry
	c.InvalidateByTag("tag-a")
	v, ok := c.Get("k")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true) after old tag invalidation, got (%d, %v)", v, ok)
	}

	// Invalidating new tag should remove it
	c.InvalidateByTag("tag-b")
	_, ok = c.Get("k")
	if ok {
		t.Fatal("expected 'k' to be invalidated by tag-b")
	}
}

// --- Concurrent Access Tests ---

func TestLRU_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := New[int, int](Options[int, int]{MaxSize: 100})

	var wg sync.WaitGroup
	const goroutines = 50
	const opsPerRoutine = 200

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerRoutine; i++ {
				key := (id*opsPerRoutine + i) % 150
				switch i % 4 {
				case 0:
					c.Set(key, id)
				case 1:
					c.Get(key)
				case 2:
					c.Delete(key)
				case 3:
					c.Peek(key)
				}
			}
		}(g)
	}

	wg.Wait()

	// No panics, no races (run with -race)
	if c.Len() < 0 {
		t.Fatal("impossible")
	}
}

func TestLRU_ConcurrentTags(t *testing.T) {
	t.Parallel()
	// MaxSize:0 (unlimited) keeps all entries so tag distribution is
	// deterministic regardless of goroutine scheduling order. With a
	// bounded cache, LRU eviction survivors depend on which goroutine
	// wrote last, making the post-invalidation assertion flaky.
	c := New[string, int](Options[string, int]{MaxSize: 0})

	var wg sync.WaitGroup
	const goroutines = 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := fmt.Sprintf("key-%d-%d", id, i)
				tag := fmt.Sprintf("tag-%d", id%5)
				c.SetWithTags(key, i, []string{tag})
			}
		}(g)
	}

	wg.Wait()

	// goroutines 0,5,10,15 write tag-0 (4 × 100 = 400 of 2000 entries).
	c.InvalidateByTag("tag-0")

	// The remaining 16 goroutines × 100 = 1600 entries must survive.
	if remaining := c.Len(); remaining != 1600 {
		t.Fatalf("expected 1600 entries after invalidating tag-0, got %d", remaining)
	}
}

// --- Edge Cases ---

func TestLRU_ZeroTTL_NoExpiry(t *testing.T) {
	t.Parallel()
	clock := newMockClock(time.Now())
	c := New[string, int](Options[string, int]{
		MaxSize: 10,
		Now:     clock.Now,
	})

	c.Set("k", 1)
	clock.Advance(24 * time.Hour * 365) // 1 year later

	v, ok := c.Get("k")
	if !ok || v != 1 {
		t.Fatalf("expected no expiry with zero TTL, got (%d, %v)", v, ok)
	}
}

func TestLRU_MaxSizeOne(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 1})

	c.Set("a", 1)
	c.Set("b", 2)

	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected 'a' evicted with MaxSize=1")
	}

	v, ok := c.Get("b")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", v, ok)
	}
}

func TestLRU_Keys_Order(t *testing.T) {
	t.Parallel()
	c := New[string, int](Options[string, int]{MaxSize: 10})

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// Access "a" to promote it
	c.Get("a")

	keys := c.Keys()
	// Expected: [a, c, b] — "a" most recent, "b" least recent
	if len(keys) != 3 || keys[0] != "a" || keys[1] != "c" || keys[2] != "b" {
		t.Fatalf("expected [a, c, b], got %v", keys)
	}
}
