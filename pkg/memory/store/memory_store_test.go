package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memory/delegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEmbedder is a simple embedding provider for testing.
type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, text string) (memory.Embedding, error) {
	return deterministicVec(text, m.dim), nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([]memory.Embedding, error) {
	results := make([]memory.Embedding, len(texts))
	for i, t := range texts {
		results[i] = deterministicVec(t, m.dim)
	}
	return results, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dim }
func (m *mockEmbedder) Model() string   { return "mock-embed" }

// deterministicVec generates a deterministic embedding vector from text (hash-based).
func deterministicVec(text string, dim int) memory.Embedding {
	vec := make(memory.Embedding, dim)
	for i := range vec {
		h := 0.0
		for j, ch := range text {
			h += float64(ch) * float64(i+1) * float64(j+1)
		}
		// Normalize to [-1, 1] range
		vec[i] = float32((float64(int(h)%2000) - 1000.0) / 1000.0)
	}
	return vec
}

func newTestStore(t *testing.T, withEmbedder bool) *MemoryStore {
	t.Helper()
	ctx := context.Background()

	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))

	chunker := NewMarkdownChunker(MarkdownChunkerConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
	})

	var emb memory.EmbeddingProvider
	if withEmbedder {
		emb = &mockEmbedder{dim: 768}
	}

	store := New(del, chunker, emb, Config{
		ContextWindowTokens:    10000,
		OffloadThresholdTokens: 100,
		DefaultHalfLifeHours:   168,
	})

	t.Cleanup(func() { store.Close() })
	return store
}

func TestWorkingContext_SetAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	// Initially empty
	content, err := store.GetWorkingContext(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Empty(t, content)

	// Set
	err = store.SetWorkingContext(ctx, "agent-1", "session-1", "You are a helpful assistant.")
	require.NoError(t, err)

	// Get back
	content, err = store.GetWorkingContext(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Equal(t, "You are a helpful assistant.", content)

	// Update
	err = store.SetWorkingContext(ctx, "agent-1", "session-1", "Updated context.")
	require.NoError(t, err)

	content, err = store.GetWorkingContext(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Equal(t, "Updated context.", content)
}

func TestWorkingContext_IsolatedBySessions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	err := store.SetWorkingContext(ctx, "agent-1", "session-a", "Context A")
	require.NoError(t, err)
	err = store.SetWorkingContext(ctx, "agent-1", "session-b", "Context B")
	require.NoError(t, err)

	a, err := store.GetWorkingContext(ctx, "agent-1", "session-a")
	require.NoError(t, err)
	assert.Equal(t, "Context A", a)

	b, err := store.GetWorkingContext(ctx, "agent-1", "session-b")
	require.NoError(t, err)
	assert.Equal(t, "Context B", b)
}

func TestRecall_CRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	item := &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.8,
		Content:    "The user asked about Go generics.",
		Tags:       "golang,generics",
	}

	// Store
	err := store.StoreRecall(ctx, item)
	require.NoError(t, err)
	assert.False(t, item.ID.IsZero(), "ID should be auto-generated")

	// Get
	got, err := store.GetRecall(ctx, item.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "The user asked about Go generics.", got.Content)
	assert.Equal(t, memory.SectorEpisodic, got.Sector)
	assert.InDelta(t, 0.8, got.Importance, 0.001)

	// Update
	got.Importance = 0.95
	got.Content = "Updated: user asked about Go generics in depth."
	err = store.UpdateRecall(ctx, got)
	require.NoError(t, err)

	updated, err := store.GetRecall(ctx, item.ID)
	require.NoError(t, err)
	assert.InDelta(t, 0.95, updated.Importance, 0.001)
	assert.Equal(t, "Updated: user asked about Go generics in depth.", updated.Content)

	// Delete
	err = store.DeleteRecall(ctx, item.ID)
	require.NoError(t, err)

	deleted, err := store.GetRecall(ctx, item.ID)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

func TestArchival_StoreAndRetrieve(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, true)

	// Store a multi-chunk document
	content := strings.Repeat("This is a test paragraph about Go programming. ", 20)
	refID, err := store.StoreArchival(ctx, content, "test-source", map[string]string{
		"agent_id":    "agent-1",
		"session_key": "session-1",
		"tags":        "test,archival",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refID)

	// Retrieve full content
	retrieved, err := store.RetrieveArchival(ctx, refID)
	require.NoError(t, err)
	assert.NotEmpty(t, retrieved)
	// Content should be reconstructable (may differ slightly due to chunk boundaries)
	assert.Contains(t, retrieved, "Go programming")
}

func TestArchival_WithoutEmbedder(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false) // no embedder

	content := "Short archival content for testing without embeddings."
	refID, err := store.StoreArchival(ctx, content, "no-embed-source", map[string]string{
		"agent_id":    "agent-1",
		"session_key": "session-1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refID)

	retrieved, err := store.RetrieveArchival(ctx, refID)
	require.NoError(t, err)
	assert.Contains(t, retrieved, "archival content")
}

func TestSearch_KeywordOnly(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	// Seed some recall items
	items := []*memory.RecallItem{
		{AgentID: "agent-1", SessionKey: "s1", Role: "user", Sector: memory.SectorSemantic, Importance: 0.9, Content: "Go generics were introduced in Go 1.18"},
		{AgentID: "agent-1", SessionKey: "s1", Role: "user", Sector: memory.SectorSemantic, Importance: 0.7, Content: "Rust has a powerful type system"},
		{AgentID: "agent-1", SessionKey: "s1", Role: "user", Sector: memory.SectorSemantic, Importance: 0.5, Content: "Python is great for prototyping"},
	}
	for _, item := range items {
		require.NoError(t, store.StoreRecall(ctx, item))
	}

	// Search for "generics"
	results, err := store.Search(ctx, "generics", memory.SearchOptions{
		AgentID: "agent-1",
		Limit:   10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	// First result should mention generics
	assert.Contains(t, results[0].Content, "generics")
}

func TestSearch_HybridWithEmbeddings(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, true)

	// Seed recall items
	items := []*memory.RecallItem{
		{AgentID: "agent-1", SessionKey: "s1", Role: "user", Sector: memory.SectorSemantic, Importance: 0.9, Content: "Go channels enable concurrent communication"},
		{AgentID: "agent-1", SessionKey: "s1", Role: "user", Sector: memory.SectorSemantic, Importance: 0.7, Content: "HTTP handlers process web requests"},
	}
	for _, item := range items {
		require.NoError(t, store.StoreRecall(ctx, item))
	}

	// Store archival content
	_, err := store.StoreArchival(ctx, "Goroutines are lightweight threads managed by the Go runtime.", "docs", map[string]string{
		"agent_id":    "agent-1",
		"session_key": "s1",
	})
	require.NoError(t, err)

	// Search should combine keyword + vector results
	results, err := store.Search(ctx, "concurrent goroutines", memory.SearchOptions{
		AgentID:       "agent-1",
		Limit:         10,
		KeywordWeight: 1.0,
		VectorWeight:  0.8,
	})
	require.NoError(t, err)
	// At minimum, keyword search should find something
	assert.NotEmpty(t, results)
}

func TestContextUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	// Empty system — should be normal pressure
	pressure, err := store.ContextUsage(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Equal(t, memory.PressureNormal, pressure.PressureLevel)
	assert.Equal(t, 0, pressure.WorkingContextTokens)
	assert.Equal(t, 0, pressure.RecallItemCount)

	// Add working context
	err = store.SetWorkingContext(ctx, "agent-1", "session-1", strings.Repeat("x", 4000))
	require.NoError(t, err)

	pressure, err = store.ContextUsage(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Greater(t, pressure.WorkingContextTokens, 0)
	assert.Greater(t, pressure.EstimatedTotalTokens, 0)
}

func TestContextUsage_PressureLevels(t *testing.T) {
	ctx := context.Background()

	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))
	defer del.Close()

	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())

	// Use a tiny context window so we can trigger pressure easily
	store := New(del, chunker, nil, Config{
		ContextWindowTokens:    100,
		OffloadThresholdTokens: 50,
	})

	// Set working context to ~80 tokens (320 chars / 4)
	err = store.SetWorkingContext(ctx, "agent-1", "s1", strings.Repeat("a", 320))
	require.NoError(t, err)

	pressure, err := store.ContextUsage(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.True(t, pressure.UsageRatio >= 0.70, "expected high usage ratio, got %f", pressure.UsageRatio)
}

func TestShouldOffload(t *testing.T) {
	store := &MemoryStore{cfg: Config{OffloadThresholdTokens: 100}}

	assert.False(t, store.ShouldOffload("short"))
	assert.True(t, store.ShouldOffload(strings.Repeat("x", 500))) // 500 chars ≈ 125 tokens
}

func TestOffloadToolResult(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	largeContent := strings.Repeat("This is a large tool result that should be offloaded. ", 20)

	refID, summary, err := store.OffloadToolResult(ctx, "file_read", largeContent, "agent-1", "session-1")
	require.NoError(t, err)
	assert.False(t, refID.IsZero())
	assert.Contains(t, summary, "Offloaded")
	assert.Contains(t, summary, "file_read")
	assert.Contains(t, summary, refID.String())

	// Retrieve the offloaded content
	retrieved, err := store.RetrieveArchival(ctx, refID)
	require.NoError(t, err)
	assert.Contains(t, retrieved, "large tool result")
}

func TestStoreSummary(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, false)

	summary := &memory.MemorySummary{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Content:    "The user discussed Go memory management and garbage collection.",
		FromMsgIdx: 0,
		ToMsgIdx:   15,
	}

	err := store.StoreSummary(ctx, summary)
	require.NoError(t, err)
	assert.False(t, summary.ID.IsZero())
}

func TestDeleteRecall_CascadesArchival(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, true)

	// Store archival content (creates recall item + archival chunks)
	refID, err := store.StoreArchival(ctx, "Content that will be deleted with all its chunks.", "cascade-test", map[string]string{
		"agent_id":    "agent-1",
		"session_key": "session-1",
	})
	require.NoError(t, err)

	// Verify it exists
	retrieved, err := store.RetrieveArchival(ctx, refID)
	require.NoError(t, err)
	assert.NotEmpty(t, retrieved)

	// Delete recall item — should cascade to archival chunks
	err = store.DeleteRecall(ctx, refID)
	require.NoError(t, err)

	// Verify it's gone
	_, err = store.RetrieveArchival(ctx, refID)
	assert.Error(t, err) // Should error because recall item and chunks are deleted
}

// --- Retrieval pipeline unit tests ---

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     memory.Embedding
		expected float64
	}{
		{"identical", memory.Embedding{1, 0, 0}, memory.Embedding{1, 0, 0}, 1.0},
		{"orthogonal", memory.Embedding{1, 0, 0}, memory.Embedding{0, 1, 0}, 0.0},
		{"opposite", memory.Embedding{1, 0, 0}, memory.Embedding{-1, 0, 0}, -1.0},
		{"empty", nil, nil, 0.0},
		{"mismatch", memory.Embedding{1, 0}, memory.Embedding{1, 0, 0}, 0.0},
		{"zero vec", memory.Embedding{0, 0, 0}, memory.Embedding{1, 0, 0}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.expected, got, 0.001)
		})
	}
}

func TestRRF_MergesTwoSets(t *testing.T) {
	idA, idB, idC := ids.New(), ids.New(), ids.New()
	set1 := []memory.SearchResult{
		{ID: idA, Content: "a", Score: 1.0},
		{ID: idB, Content: "b", Score: 0.8},
	}
	set2 := []memory.SearchResult{
		{ID: idB, Content: "b", Score: 1.0},
		{ID: idC, Content: "c", Score: 0.5},
	}

	merged := ReciprocalRankFusion([][]memory.SearchResult{set1, set2}, []float64{1.0, 1.0}, 60)
	require.GreaterOrEqual(t, len(merged), 2)
	// idB appears in both sets, should have highest fused score
	assert.Equal(t, idB, merged[0].ID)
}

func TestRecencyDecay(t *testing.T) {
	// 0 hours age → decay = 1.0
	assert.InDelta(t, 1.0, RecencyDecay(0, 168), 0.001)

	// 168 hours (1 half-life) → decay = 0.5
	assert.InDelta(t, 0.5, RecencyDecay(168*time.Hour, 168), 0.001)

	// 336 hours (2 half-lives) → decay = 0.25
	assert.InDelta(t, 0.25, RecencyDecay(336*time.Hour, 168), 0.001)
}

func TestApplyRecencyDecay_ReordersByAge(t *testing.T) {
	now := time.Now()
	idOld, idNew := ids.New(), ids.New()

	results := []memory.SearchResult{
		{ID: idOld, Content: "old", Score: 1.0},
		{ID: idNew, Content: "new", Score: 0.9},
	}

	createdAt := map[ids.UUID]time.Time{
		idOld: now.Add(-720 * time.Hour), // 30 days old
		idNew: now.Add(-1 * time.Hour),   // 1 hour old
	}

	ApplyRecencyDecay(results, now, 168, func(id ids.UUID) time.Time {
		return createdAt[id]
	})

	// idNew should now rank higher because idOld got heavily decayed
	assert.Equal(t, idNew, results[0].ID)
}
