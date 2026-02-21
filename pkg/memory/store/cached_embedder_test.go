package store

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

type countingEmbedder struct {
	embedCalls atomic.Int64
	batchCalls atomic.Int64
	dims       int
}

func (e *countingEmbedder) Embed(_ context.Context, text string) (memory.Embedding, error) {
	e.embedCalls.Add(1)
	// Deterministic embedding based on text length
	vec := make(memory.Embedding, e.dims)
	for i := range vec {
		vec[i] = float32(len(text)+i) * 0.01
	}
	return vec, nil
}

func (e *countingEmbedder) EmbedBatch(_ context.Context, texts []string) ([]memory.Embedding, error) {
	e.batchCalls.Add(1)
	results := make([]memory.Embedding, len(texts))
	for i, text := range texts {
		vec := make(memory.Embedding, e.dims)
		for j := range vec {
			vec[j] = float32(len(text)+j) * 0.01
		}
		results[i] = vec
	}
	return results, nil
}

func (e *countingEmbedder) Dimensions() int { return e.dims }
func (e *countingEmbedder) Model() string   { return "test-model" }

func TestCachedEmbedder_CachesIdenticalText(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 8}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	ctx := t.Context()

	// First call — should hit inner
	vec1, err := cached.Embed(ctx, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if inner.embedCalls.Load() != 1 {
		t.Fatalf("expected 1 inner call, got %d", inner.embedCalls.Load())
	}

	// Second call with same text — should hit cache
	vec2, err := cached.Embed(ctx, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if inner.embedCalls.Load() != 1 {
		t.Fatalf("expected still 1 inner call, got %d", inner.embedCalls.Load())
	}

	// Vectors should be identical
	if len(vec1) != len(vec2) {
		t.Fatal("vector lengths differ")
	}
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("vec[%d] differs: %f vs %f", i, vec1[i], vec2[i])
		}
	}
}

func TestCachedEmbedder_DifferentTextHitsInner(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 4}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	ctx := t.Context()

	cached.Embed(ctx, "text A")
	cached.Embed(ctx, "text B")
	cached.Embed(ctx, "text C")

	if inner.embedCalls.Load() != 3 {
		t.Fatalf("expected 3 inner calls, got %d", inner.embedCalls.Load())
	}
	if cached.CacheLen() != 3 {
		t.Fatalf("expected 3 cached entries, got %d", cached.CacheLen())
	}
}

func TestCachedEmbedder_BatchPartialCache(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 4}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	ctx := t.Context()

	// Pre-cache one text
	cached.Embed(ctx, "cached text")
	if inner.embedCalls.Load() != 1 {
		t.Fatal("expected 1 inner call")
	}

	// Batch with one cached + two uncached
	vecs, err := cached.EmbedBatch(ctx, []string{"cached text", "new A", "new B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}

	// Only one batch call for the 2 uncached texts
	if inner.batchCalls.Load() != 1 {
		t.Fatalf("expected 1 batch call, got %d", inner.batchCalls.Load())
	}

	// All results should be non-nil
	for i, vec := range vecs {
		if vec == nil {
			t.Errorf("vector %d is nil", i)
		}
	}
}

func TestCachedEmbedder_BatchAllCached(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 4}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	ctx := t.Context()

	// Pre-cache all texts
	cached.Embed(ctx, "A")
	cached.Embed(ctx, "B")

	// Batch should not call inner at all
	vecs, err := cached.EmbedBatch(ctx, []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2, got %d", len(vecs))
	}
	if inner.batchCalls.Load() != 0 {
		t.Fatalf("expected 0 batch calls, got %d", inner.batchCalls.Load())
	}
}

func TestCachedEmbedder_Dimensions(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 768}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	if cached.Dimensions() != 768 {
		t.Errorf("expected 768, got %d", cached.Dimensions())
	}
}

func TestCachedEmbedder_Model(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 4}
	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())
	if cached.Model() != "test-model" {
		t.Errorf("expected test-model, got %s", cached.Model())
	}
}

func TestCachedEmbedder_MaxEntries(t *testing.T) {
	t.Parallel()
	inner := &countingEmbedder{dims: 4}
	cached := NewCachedEmbedder(inner, CachedEmbedderConfig{
		MaxEntries: 3,
		TTL:        time.Hour,
	})
	ctx := t.Context()

	// Fill cache
	cached.Embed(ctx, "A")
	cached.Embed(ctx, "B")
	cached.Embed(ctx, "C")
	cached.Embed(ctx, "D") // This should evict "A"

	if cached.CacheLen() != 3 {
		t.Fatalf("expected 3 entries, got %d", cached.CacheLen())
	}

	// "A" should miss (evicted)
	callsBefore := inner.embedCalls.Load()
	cached.Embed(ctx, "A")
	if inner.embedCalls.Load() != callsBefore+1 {
		t.Error("expected A to be re-embedded after eviction")
	}

	// "D" should hit (still in cache)
	callsBefore = inner.embedCalls.Load()
	cached.Embed(ctx, "D")
	if inner.embedCalls.Load() != callsBefore {
		t.Error("expected D to hit cache")
	}
}
