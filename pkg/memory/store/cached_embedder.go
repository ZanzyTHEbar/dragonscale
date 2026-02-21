package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/cache"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// CachedEmbedder wraps an EmbeddingProvider with an LRU cache keyed by content hash.
// Identical text is never re-embedded — the cached vector is returned instead.
type CachedEmbedder struct {
	inner memory.EmbeddingProvider
	cache *cache.LRU[string, memory.Embedding]
}

// CachedEmbedderConfig configures the embedding cache.
type CachedEmbedderConfig struct {
	// MaxEntries is the maximum number of embedding vectors to cache.
	// Default: 2048
	MaxEntries int

	// TTL is how long a cached embedding stays valid. Zero means no expiration.
	// Default: 1 hour
	TTL time.Duration
}

// DefaultCachedEmbedderConfig returns sensible defaults.
func DefaultCachedEmbedderConfig() CachedEmbedderConfig {
	return CachedEmbedderConfig{
		MaxEntries: 2048,
		TTL:        time.Hour,
	}
}

// NewCachedEmbedder wraps an EmbeddingProvider with an LRU cache.
func NewCachedEmbedder(inner memory.EmbeddingProvider, cfg CachedEmbedderConfig) *CachedEmbedder {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 2048
	}
	return &CachedEmbedder{
		inner: inner,
		cache: cache.New(cache.Options[string, memory.Embedding]{
			MaxSize: cfg.MaxEntries,
			TTL:     cfg.TTL,
		}),
	}
}

func (c *CachedEmbedder) Embed(ctx context.Context, text string) (memory.Embedding, error) {
	key := contentHash(text)

	if vec, ok := c.cache.Get(key); ok {
		return vec, nil
	}

	vec, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	c.cache.Set(key, vec)
	return vec, nil
}

func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	results := make([]memory.Embedding, len(texts))
	var uncached []string
	var uncachedIdx []int

	for i, text := range texts {
		key := contentHash(text)
		if vec, ok := c.cache.Get(key); ok {
			results[i] = vec
		} else {
			uncached = append(uncached, text)
			uncachedIdx = append(uncachedIdx, i)
		}
	}

	if len(uncached) == 0 {
		return results, nil
	}

	// Embed only the uncached texts
	vecs, err := c.inner.EmbedBatch(ctx, uncached)
	if err != nil {
		return nil, err
	}

	for j, vec := range vecs {
		idx := uncachedIdx[j]
		results[idx] = vec
		c.cache.Set(contentHash(uncached[j]), vec)
	}

	return results, nil
}

func (c *CachedEmbedder) Dimensions() int { return c.inner.Dimensions() }
func (c *CachedEmbedder) Model() string   { return c.inner.Model() }

// CacheLen returns the current number of cached embeddings.
func (c *CachedEmbedder) CacheLen() int { return c.cache.Len() }

// contentHash returns a SHA-256 hex digest of the text, used as cache key.
func contentHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}
