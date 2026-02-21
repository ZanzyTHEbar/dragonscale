package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memdag "github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEmbedder is a simple embedding provider for testing.
type mockEmbedder struct {
	dim int
}

type failingKVDelegate struct {
	memory.MemoryDelegate
	failKeys map[string]error
}

func (d *failingKVDelegate) UpsertKV(ctx context.Context, agentID, key, value string) error {
	if d.failKeys != nil {
		if err, ok := d.failKeys[key]; ok {
			return err
		}
	}
	return d.MemoryDelegate.UpsertKV(ctx, agentID, key, value)
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
	ctx := t.Context()

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
	store.SetAgentID("agent-1")

	t.Cleanup(func() { store.Close() })
	return store
}

func TestWorkingContext_SetAndGet(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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

func TestSearch_ShadowModeUsesBaselineAndTracksParity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, false)

	// Baseline recall hit.
	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Role:       "user",
		Sector:     memory.SectorSemantic,
		Importance: 0.8,
		Content:    "Grocery checklist includes milk and eggs.",
	}))

	// Augmented projection hit (would win if promoted).
	require.NoError(t, store.SetWorkingContext(ctx, "agent-1", "s1", "Weekly planning notes include grocery tasks and laundry reminders."))

	results, err := store.Search(ctx, "grocery", memory.SearchOptions{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Limit:      5,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	// Shadow mode should keep baseline result ordering for production output.
	assert.NotContains(t, results[0].Source, "working-context:s1")

	metricsRaw, err := store.delegate.GetKV(ctx, "agent-1", retrievalPolicyMetricsKey)
	require.NoError(t, err)
	require.NotEmpty(t, metricsRaw)

	var metrics retrievalShadowMetrics
	require.NoError(t, json.Unmarshal([]byte(metricsRaw), &metrics))
	assert.Equal(t, 1, metrics.TotalQueries)
}

func TestSearch_DoesNotPromoteWithoutAugmentedSignals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, false)

	gates := retrievalPromotionGates{
		MinSamples:           1,
		MinAugmentedSamples:  1,
		MinTop1Parity:        0.0001,
		MinOverlapAtK:        0.0001,
		MinPromotedSamples:   1,
		RollbackTop1Parity:   0.0001,
		RollbackOverlapAtK:   0.0001,
		MaxNoResultRate:      1.0,
		PromotedNoResultRate: 1.0,
	}
	gatesPayload, err := json.Marshal(gates)
	require.NoError(t, err)
	require.NoError(t, store.delegate.UpsertKV(ctx, "agent-1", retrievalPolicyGatesKey, string(gatesPayload)))

	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Role:       "user",
		Sector:     memory.SectorSemantic,
		Importance: 0.8,
		Content:    "Baseline-only query signal.",
	}))

	_, err = store.Search(ctx, "baseline-only", memory.SearchOptions{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Limit:      5,
	})
	require.NoError(t, err)

	stateRaw, err := store.delegate.GetKV(ctx, "agent-1", retrievalPolicyStateKey)
	require.NoError(t, err)
	var state retrievalPolicyState
	require.NoError(t, json.Unmarshal([]byte(stateRaw), &state))
	assert.Equal(t, retrievalModeShadow, state.Mode)
}

func TestSearch_PromoteOnlyOnGateWin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, false)

	gates := retrievalPromotionGates{
		MinSamples:           1,
		MinAugmentedSamples:  1,
		MinTop1Parity:        0.0001,
		MinOverlapAtK:        0.0001,
		MinPromotedSamples:   1,
		RollbackTop1Parity:   0.0001,
		RollbackOverlapAtK:   0.0001,
		MaxNoResultRate:      1.0,
		PromotedNoResultRate: 1.0,
	}
	gatesPayload, err := json.Marshal(gates)
	require.NoError(t, err)
	require.NoError(t, store.delegate.UpsertKV(ctx, "agent-1", retrievalPolicyGatesKey, string(gatesPayload)))

	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Role:       "user",
		Sector:     memory.SectorSemantic,
		Importance: 0.8,
		Content:    "Baseline grocery memory entry.",
	}))
	require.NoError(t, store.SetWorkingContext(ctx, "agent-1", "s1", "Augmented grocery projection content."))

	_, err = store.Search(ctx, "grocery", memory.SearchOptions{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Limit:      5,
	})
	require.NoError(t, err)

	stateRaw, err := store.delegate.GetKV(ctx, "agent-1", retrievalPolicyStateKey)
	require.NoError(t, err)
	require.NotEmpty(t, stateRaw)

	var state retrievalPolicyState
	require.NoError(t, json.Unmarshal([]byte(stateRaw), &state))
	assert.Equal(t, retrievalModePromoted, state.Mode)
}

func TestSearch_FastRollbackPreservesBaselinePath(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, false)

	del, ok := store.delegate.(*delegate.LibSQLDelegate)
	require.True(t, ok)

	gates := retrievalPromotionGates{
		MinSamples:           1,
		MinAugmentedSamples:  1,
		MinTop1Parity:        0.0001,
		MinOverlapAtK:        0.0001,
		MinPromotedSamples:   1,
		RollbackTop1Parity:   1.0, // Force rollback if top-1 diverges.
		RollbackOverlapAtK:   1.0, // Force rollback if overlap is not perfect.
		MaxNoResultRate:      1.0,
		PromotedNoResultRate: 1.0,
	}
	gatesPayload, err := json.Marshal(gates)
	require.NoError(t, err)
	require.NoError(t, store.delegate.UpsertKV(ctx, "agent-1", retrievalPolicyGatesKey, string(gatesPayload)))

	statePayload, err := json.Marshal(retrievalPolicyState{
		Mode:      retrievalModePromoted,
		Reason:    "test_bootstrap",
		UpdatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, store.delegate.UpsertKV(ctx, "agent-1", retrievalPolicyStateKey, string(statePayload)))

	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Role:       "user",
		Sector:     memory.SectorSemantic,
		Importance: 0.8,
		Content:    "Baseline grocery memory entry.",
	}))

	snap := &memdag.PersistSnapshot{
		FromMsgIdx: 0,
		ToMsgIdx:   4,
		MsgCount:   4,
		DAG: &memdag.DAG{
			Nodes: map[string]*memdag.Node{
				"n-session": {
					ID:       "n-session",
					Level:    memdag.LevelSession,
					Summary:  "Household grocery commitments with Friday reminder schedule.",
					Tokens:   12,
					StartIdx: 0,
					EndIdx:   4,
				},
			},
			Roots: []string{"n-session"},
		},
	}
	require.NoError(t, del.PersistDAG(ctx, "agent-1", "s1", snap))
	require.NoError(t, store.SetWorkingContext(ctx, "agent-1", "s1", "Working context grocery signal."))

	// First call in promoted mode should evaluate and trigger rollback.
	_, err = store.Search(ctx, "grocery", memory.SearchOptions{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Limit:      10,
	})
	require.NoError(t, err)

	stateRaw, err := store.delegate.GetKV(ctx, "agent-1", retrievalPolicyStateKey)
	require.NoError(t, err)
	var state retrievalPolicyState
	require.NoError(t, json.Unmarshal([]byte(stateRaw), &state))
	assert.Equal(t, retrievalModeRollback, state.Mode)

	// Subsequent calls should return baseline-only path again.
	results, err := store.Search(ctx, "grocery", memory.SearchOptions{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Limit:      10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.NotContains(t, results[0].Source, "working-context:")
	assert.NotContains(t, results[0].Source, "dag:")
}

func TestUpdateRetrievalPolicy_PersistFailuresDoNotBlockTransitions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, true)

	baseDelegate := store.delegate
	store.delegate = &failingKVDelegate{
		MemoryDelegate: baseDelegate,
		failKeys: map[string]error{
			retrievalPolicyStateKey:   errors.New("state kv unavailable"),
			retrievalPolicyMetricsKey: errors.New("metrics kv unavailable"),
		},
	}

	state := defaultRetrievalPolicyState()
	gates := retrievalPromotionGates{
		MinSamples:           1,
		MinAugmentedSamples:  1,
		MinTop1Parity:        0.5,
		MinOverlapAtK:        0.5,
		MinPromotedSamples:   1,
		RollbackTop1Parity:   0.1,
		RollbackOverlapAtK:   0.1,
		MaxNoResultRate:      1.0,
		PromotedNoResultRate: 1.0,
	}
	metrics := defaultRetrievalShadowMetrics()
	parity := retrievalParity{
		Top1Match:  true,
		OverlapAtK: 1.0,
	}
	baseline := []memory.SearchResult{
		{ID: ids.New(), Content: "baseline"},
	}

	next := store.updateRetrievalPolicy(ctx, state, gates, metrics, parity, true, baseline)
	assert.Equal(t, retrievalModePromoted, next.Mode)
}

func TestSearch_ConcurrentRetrievalPolicyUpdates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, true)

	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "s1",
		Role:       "user",
		Sector:     memory.SectorSemantic,
		Importance: 0.9,
		Content:    "Grocery planning memory for retrieval policy concurrency tests.",
		Tags:       "grocery",
	}))

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Search(ctx, "grocery", memory.SearchOptions{
				AgentID:    "agent-1",
				SessionKey: "s1",
				Limit:      10,
			})
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	metricsRaw, err := store.delegate.GetKV(ctx, "agent-1", retrievalPolicyMetricsKey)
	require.NoError(t, err)
	var metrics retrievalShadowMetrics
	require.NoError(t, json.Unmarshal([]byte(metricsRaw), &metrics))
	assert.GreaterOrEqual(t, metrics.TotalQueries, workers)
}

func TestContextUsage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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

func TestContextUsage_RecallTokenEstimateUsesContent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTestStore(t, false)

	contentA := strings.Repeat("schedule follow-up reminder ", 80)
	contentB := strings.Repeat("prepare project update summary ", 80)

	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.7,
		Content:    contentA,
		Tags:       "reminder",
	}))
	require.NoError(t, store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "assistant",
		Sector:     memory.SectorEpisodic,
		Importance: 0.7,
		Content:    contentB,
		Tags:       "summary",
	}))

	pressure, err := store.ContextUsage(ctx, "agent-1", "session-1")
	require.NoError(t, err)

	expectedRecallTokens := estimateTokens(contentA) + estimateTokens(contentB)
	assert.GreaterOrEqual(t, pressure.EstimatedTotalTokens, expectedRecallTokens)
	assert.Equal(t, 2, pressure.RecallItemCount)
}

func TestContextUsage_PressureLevels(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

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

	// Set working context to ~80 tokens.
	err = store.SetWorkingContext(ctx, "agent-1", "s1", contentAtLeastTokens(80))
	require.NoError(t, err)

	pressure, err := store.ContextUsage(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.True(t, pressure.UsageRatio >= 0.70, "expected high usage ratio, got %f", pressure.UsageRatio)
}

func TestShouldOffload(t *testing.T) {
	t.Parallel()
	store := &MemoryStore{cfg: Config{OffloadThresholdTokens: 100}}

	assert.False(t, store.ShouldOffload("short"))
	assert.True(t, store.ShouldOffload(contentAtLeastTokens(120)))
}

func TestOffloadToolResult(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
	ctx := t.Context()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel(
	// 0 hours age → decay = 1.0
	)

	assert.InDelta(t, 1.0, RecencyDecay(0, 168), 0.001)

	// 168 hours (1 half-life) → decay = 0.5
	assert.InDelta(t, 0.5, RecencyDecay(168*time.Hour, 168), 0.001)

	// 336 hours (2 half-lives) → decay = 0.25
	assert.InDelta(t, 0.25, RecencyDecay(336*time.Hour, 168), 0.001)
}

func TestApplyRecencyDecay_ReordersByAge(t *testing.T) {
	t.Parallel()
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
