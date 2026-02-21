package store

import (
	"context"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestQueueManager(t *testing.T, contextWindow int) (*QueueManager, *MemoryStore) {
	t.Helper()
	ctx := context.Background()

	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))

	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())

	store := New(del, chunker, nil, Config{
		ContextWindowTokens:    contextWindow,
		OffloadThresholdTokens: 100,
	})

	qm := NewQueueManager(store, DefaultQueueManagerConfig())
	t.Cleanup(func() { store.Close() })
	return qm, store
}

func TestQueueManager_NormalPressure(t *testing.T) {
	ctx := context.Background()
	qm, _ := newTestQueueManager(t, 100000)

	decision, err := qm.Evaluate(ctx, "agent-1", "session-1")
	require.NoError(t, err)
	assert.Equal(t, QueueActionNone, decision.Action)
	assert.Contains(t, decision.Message, "healthy")
}

func TestQueueManager_WarnPressure(t *testing.T) {
	ctx := context.Background()
	qm, store := newTestQueueManager(t, 100) // tiny window

	// Fill working context to ~75% of context window.
	err := store.SetWorkingContext(ctx, "agent-1", "s1", contentAtLeastTokens(75))
	require.NoError(t, err)

	decision, err := qm.Evaluate(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.Equal(t, QueueActionWarn, decision.Action)
	assert.Contains(t, decision.Message, "selective")
}

func TestQueueManager_OffloadPressure(t *testing.T) {
	ctx := context.Background()
	qm, store := newTestQueueManager(t, 100) // tiny window

	// Fill to ~82% of context window.
	err := store.SetWorkingContext(ctx, "agent-1", "s1", contentAtLeastTokens(82))
	require.NoError(t, err)

	decision, err := qm.Evaluate(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.Equal(t, QueueActionOffload, decision.Action)
	assert.Contains(t, decision.Message, "offloading")
}

func TestQueueManager_FlushPressure(t *testing.T) {
	ctx := context.Background()
	qm, store := newTestQueueManager(t, 100) // tiny window

	// Fill to ~88% of context window.
	err := store.SetWorkingContext(ctx, "agent-1", "s1", contentAtLeastTokens(88))
	require.NoError(t, err)

	decision, err := qm.Evaluate(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.Equal(t, QueueActionFlush, decision.Action)
	assert.Contains(t, decision.Message, "flush")
}

func TestQueueManager_EvictOldest(t *testing.T) {
	ctx := context.Background()
	qm, store := newTestQueueManager(t, 100)

	// Seed recall items
	for i := 0; i < 5; i++ {
		item := &memory.RecallItem{
			AgentID:    "agent-1",
			SessionKey: "s1",
			Role:       "user",
			Sector:     memory.SectorEpisodic,
			Importance: 0.3,
			Content:    strings.Repeat("item content ", 5),
		}
		require.NoError(t, store.StoreRecall(ctx, item))
	}

	// Evict oldest
	evicted, summary, err := qm.EvictOldest(ctx, "agent-1", "s1")
	require.NoError(t, err)
	assert.Greater(t, evicted, 0)
	assert.Contains(t, summary, "Memory compaction")
}

func TestQueueManager_EvictEmpty(t *testing.T) {
	ctx := context.Background()
	qm, _ := newTestQueueManager(t, 100)

	evicted, summary, err := qm.EvictOldest(ctx, "agent-1", "empty-session")
	require.NoError(t, err)
	assert.Equal(t, 0, evicted)
	assert.Empty(t, summary)
}

func TestDefaultQueueManagerConfig(t *testing.T) {
	cfg := DefaultQueueManagerConfig()
	assert.InDelta(t, 0.70, cfg.WarnThreshold, 0.001)
	assert.InDelta(t, 0.80, cfg.OffloadThreshold, 0.001)
	assert.InDelta(t, 0.85, cfg.FlushThreshold, 0.001)
	assert.Equal(t, 10, cfg.MaxEvictBatch)
}
