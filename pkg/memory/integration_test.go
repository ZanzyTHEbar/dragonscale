//go:build integration

package memory_test

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memory/delegate"
	memstore "github.com/sipeed/picoclaw/pkg/memory/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAgent   = "integration-agent"
	testSession = "integration-sess"
)

func setupFullStack(t *testing.T) (*memstore.MemoryStore, *delegate.LibSQLDelegate) {
	t.Helper()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err, "create in-memory delegate")
	require.NoError(t, del.Init(context.Background()), "run goose migrations")
	t.Cleanup(func() { del.Close() })

	chunker := memstore.NewMarkdownChunker(memstore.DefaultMarkdownChunkerConfig())
	store := memstore.New(del, chunker, nil, memstore.DefaultConfig())
	store.SetAgentID(testAgent)
	return store, del
}

func TestIntegration_GooseMigrationIdempotent(t *testing.T) {
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	defer del.Close()

	ctx := context.Background()
	require.NoError(t, del.Init(ctx), "first migration up")
	require.NoError(t, del.Init(ctx), "idempotent re-Init")
}

func TestIntegration_GooseMigration_DownUpRoundTrip(t *testing.T) {
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	defer del.Close()

	ctx := context.Background()

	require.NoError(t, del.Init(ctx), "initial up")

	item := &memory.RecallItem{
		AgentID:    testAgent,
		SessionKey: testSession,
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Content:    "pre-migration item",
	}
	chunker := memstore.NewMarkdownChunker(memstore.DefaultMarkdownChunkerConfig())
	store := memstore.New(del, chunker, nil, memstore.DefaultConfig())
	store.SetAgentID(testAgent)
	require.NoError(t, store.StoreRecall(ctx, item))

	require.NoError(t, del.MigrateDown(ctx), "down migration should succeed")

	require.NoError(t, del.Init(ctx), "re-up after down should succeed")

	fetched, err := store.GetRecall(ctx, item.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched, "data should be gone after down+up round-trip")

	newItem := &memory.RecallItem{
		AgentID:    testAgent,
		SessionKey: testSession,
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Content:    "post-migration item",
	}
	require.NoError(t, store.StoreRecall(ctx, newItem), "should be able to write after re-migration")
	assert.False(t, newItem.ID.IsZero())
}

func TestIntegration_BlobPK_RoundTrip(t *testing.T) {
	store, _ := setupFullStack(t)
	ctx := context.Background()

	item := &memory.RecallItem{
		AgentID:    testAgent,
		SessionKey: testSession,
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.7,
		Content:    "BLOB PK integration test item",
	}

	require.NoError(t, store.StoreRecall(ctx, item))
	assert.False(t, item.ID.IsZero(), "ID should be assigned after store")

	fetched, err := store.GetRecall(ctx, item.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, item.ID, fetched.ID, "BLOB PK round-trip must preserve ID")
	assert.Equal(t, "BLOB PK integration test item", fetched.Content)
}

func TestIntegration_ArchivalChunking_EndToEnd(t *testing.T) {
	store, del := setupFullStack(t)
	ctx := context.Background()

	longContent := "# Architecture Notes\n\nThe system uses hexagonal architecture with ports and adapters.\n\n"
	longContent += "## Database Layer\n\nWe use libSQL with BLOB primary keys for storage efficiency.\n\n"
	longContent += "## Memory System\n\nThree-tier MemGPT: working context, recall, archival.\n"

	recallID, err := store.StoreArchival(ctx, longContent, testAgent, map[string]string{
		"agent_id":    testAgent,
		"session_key": testSession,
	})
	require.NoError(t, err)
	assert.False(t, recallID.IsZero(), "StoreArchival should return a valid recall ID")

	chunks, err := del.ListArchivalChunks(ctx, testAgent, recallID)
	require.NoError(t, err)
	assert.NotEmpty(t, chunks, "should create at least one archival chunk")

	for _, chunk := range chunks {
		assert.False(t, chunk.ID.IsZero(), "chunk ID should not be zero")
		assert.Equal(t, recallID, chunk.RecallID, "chunk must reference parent recall item")
	}
}

func TestIntegration_FTS5Search(t *testing.T) {
	store, _ := setupFullStack(t)
	ctx := context.Background()

	items := []struct {
		content string
		role    string
	}{
		{"The quick brown fox jumps over the lazy dog", "user"},
		{"Implementing vector search with F32_BLOB embeddings", "assistant"},
		{"Goose migrations handle schema versioning", "assistant"},
		{"The user prefers dark mode in their IDE", "user"},
	}
	for _, it := range items {
		ri := &memory.RecallItem{
			AgentID:    testAgent,
			SessionKey: testSession,
			Role:       it.role,
			Sector:     memory.SectorEpisodic,
			Content:    it.content,
		}
		require.NoError(t, store.StoreRecall(ctx, ri))
	}

	results, err := store.Search(ctx, "vector embeddings", memory.SearchOptions{
		AgentID: testAgent,
		Limit:   10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, results, "FTS5 should return results for 'vector embeddings'")

	found := false
	for _, r := range results {
		if r.Content == "Implementing vector search with F32_BLOB embeddings" {
			found = true
			break
		}
	}
	assert.True(t, found, "FTS5 should find the vector search item")
}

func TestIntegration_CascadeDelete(t *testing.T) {
	store, del := setupFullStack(t)
	ctx := context.Background()

	recallID, err := store.StoreArchival(ctx, "Archival content to be cascade deleted", testAgent, map[string]string{
		"agent_id":    testAgent,
		"session_key": testSession,
	})
	require.NoError(t, err)

	chunks, err := del.ListArchivalChunks(ctx, testAgent, recallID)
	require.NoError(t, err)
	assert.NotEmpty(t, chunks)

	require.NoError(t, store.DeleteRecall(ctx, recallID))

	for _, chunk := range chunks {
		fetched, err := del.GetArchivalChunk(ctx, testAgent, chunk.ID)
		require.NoError(t, err)
		assert.Nil(t, fetched, "archival chunks should be cascade-deleted with parent recall item")
	}
}

func TestIntegration_ToolOffload(t *testing.T) {
	store, _ := setupFullStack(t)
	ctx := context.Background()

	largeResult := ""
	for i := 0; i < 500; i++ {
		largeResult += "This is a line of tool output that contributes to the total size. "
	}

	refID, summary, err := store.OffloadToolResult(ctx, "code_search", largeResult, testAgent, testSession)
	require.NoError(t, err)
	assert.False(t, refID.IsZero(), "offload should return a valid recall ID")
	assert.NotEmpty(t, summary, "summary should be non-empty")
	assert.Contains(t, summary, refID.String(), "summary should reference the recall ID")
}

func TestIntegration_WorkingContext_Persistence(t *testing.T) {
	_, del := setupFullStack(t)
	ctx := context.Background()

	require.NoError(t, del.UpsertWorkingContext(ctx, testAgent, testSession, "initial state"))

	wc, err := del.GetWorkingContext(ctx, testAgent, testSession)
	require.NoError(t, err)
	require.NotNil(t, wc)
	assert.Equal(t, "initial state", wc.Content)

	require.NoError(t, del.UpsertWorkingContext(ctx, testAgent, testSession, "updated state"))
	wc, err = del.GetWorkingContext(ctx, testAgent, testSession)
	require.NoError(t, err)
	assert.Equal(t, "updated state", wc.Content)
}

func TestIntegration_Summary_CRUD(t *testing.T) {
	store, del := setupFullStack(t)
	ctx := context.Background()

	summary := &memory.MemorySummary{
		AgentID:    testAgent,
		SessionKey: testSession,
		Content:    "Summarized conversation about testing",
		FromMsgIdx: 0,
		ToMsgIdx:   10,
	}
	require.NoError(t, store.StoreSummary(ctx, summary))
	assert.False(t, summary.ID.IsZero(), "summary should have ID assigned")

	fetched, err := del.ListSummaries(ctx, testAgent, testSession, 1)
	require.NoError(t, err)
	require.Len(t, fetched, 1)
	assert.Equal(t, summary.ID, fetched[0].ID)
	assert.Equal(t, "Summarized conversation about testing", fetched[0].Content)
}

func TestIntegration_IDUniqueness_AcrossEntities(t *testing.T) {
	store, _ := setupFullStack(t)
	ctx := context.Background()

	seenIDs := make(map[ids.UUID]string)

	for i := 0; i < 5; i++ {
		item := &memory.RecallItem{
			AgentID:    testAgent,
			SessionKey: testSession,
			Role:       "user",
			Sector:     memory.SectorEpisodic,
			Content:    "uniqueness test item",
		}
		require.NoError(t, store.StoreRecall(ctx, item))
		if prev, exists := seenIDs[item.ID]; exists {
			t.Fatalf("duplicate ID %s: recall item collides with %s", item.ID, prev)
		}
		seenIDs[item.ID] = "recall"
	}

	summary := &memory.MemorySummary{
		AgentID:    testAgent,
		SessionKey: testSession,
		Content:    "summary for uniqueness test",
	}
	require.NoError(t, store.StoreSummary(ctx, summary))
	if prev, exists := seenIDs[summary.ID]; exists {
		t.Fatalf("summary ID %s collides with %s", summary.ID, prev)
	}
	seenIDs[summary.ID] = "summary"

	assert.Len(t, seenIDs, 6, "all 6 entities should have unique IDs")
}

func TestIntegration_ContextPressure(t *testing.T) {
	store, _ := setupFullStack(t)
	ctx := context.Background()

	pressure, err := store.ContextUsage(ctx, testAgent, testSession)
	require.NoError(t, err)
	require.NotNil(t, pressure)
	assert.Equal(t, 0, pressure.RecallItemCount)

	for i := 0; i < 3; i++ {
		item := &memory.RecallItem{
			AgentID:    testAgent,
			SessionKey: testSession,
			Role:       "user",
			Sector:     memory.SectorEpisodic,
			Content:    "pressure test item with some content",
		}
		require.NoError(t, store.StoreRecall(ctx, item))
	}

	pressure, err = store.ContextUsage(ctx, testAgent, testSession)
	require.NoError(t, err)
	assert.Equal(t, 3, pressure.RecallItemCount)
}
