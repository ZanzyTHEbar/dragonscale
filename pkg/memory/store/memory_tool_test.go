package store

import (
	"strings"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMemoryTool(t *testing.T) *MemoryTool {
	t.Helper()
	store := newTestStore(t, false)
	return NewMemoryTool(store, "agent-1", "session-1")
}

func executeAndParse(t *testing.T, tool *MemoryTool, input string) *MemoryToolResponse {
	t.Helper()
	ctx := t.Context()
	raw, err := tool.Execute(ctx, input)
	require.NoError(t, err)

	var resp MemoryToolResponse
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &resp))
	return &resp
}

func TestMemoryTool_WriteAndRead(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	// Write
	resp := executeAndParse(t, tool, `{"action":"write","content":"Go interfaces are implicitly implemented.","sector":"semantic","tags":"golang"}`)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "recall tier")

	// Extract ID from message
	// Message format: "Stored in recall tier with ID: <uuid>"
	var id string
	for _, part := range []string{resp.Message} {
		if idx := len("Stored in recall tier with ID: "); len(part) > idx {
			id = part[idx:]
		}
	}
	require.NotEmpty(t, id)

	// Read
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	assert.True(t, readResp.Success)
	require.Len(t, readResp.Results, 1)
	assert.Contains(t, readResp.Results[0].Content, "interfaces")
}

func TestMemoryTool_WriteArchival(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"write","content":"Large document content for archival.","tier":"archival","source":"test"}`)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "archival tier")
}

func TestMemoryTool_Search(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	// Seed data
	executeAndParse(t, tool, `{"action":"write","content":"Go channels enable concurrent communication between goroutines.","sector":"semantic"}`)
	executeAndParse(t, tool, `{"action":"write","content":"Python uses asyncio for asynchronous programming.","sector":"semantic"}`)

	// Search
	resp := executeAndParse(t, tool, `{"action":"search","query":"goroutines","limit":5}`)
	assert.True(t, resp.Success)
	assert.NotEmpty(t, resp.Results)
	assert.Contains(t, resp.Results[0].Content, "goroutines")
}

func TestMemoryTool_SearchNoResultsUsesExplicitEmptyMessage(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"search","query":"xyzzy_nonexistent_topic_42","limit":5}`)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Results)
	assert.Contains(t, resp.Message, "No results found for:")
}

func TestMemoryTool_SearchSuppressesPromptEchoArtifacts(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)
	ctx := t.Context()

	promptEcho := "Search your memory for 'xyzzy_nonexistent_topic_42' and tell me what you find."
	require.NoError(t, tool.store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.5,
		Content:    promptEcho,
		Tags:       "session-message",
	}))
	require.NoError(t, tool.store.SetWorkingContext(ctx, "agent-1", "session-1", promptEcho))

	resp := executeAndParse(t, tool, `{"action":"search","query":"xyzzy_nonexistent_topic_42","limit":5}`)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Results)
	assert.Contains(t, resp.Message, "No results found for:")
}

func TestMemoryTool_SearchKeepsRealMemoryWhenPromptEchoExists(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)
	ctx := t.Context()

	promptEcho := "Search your memory for 'xyzzy_nonexistent_topic_42' and tell me what you find."
	require.NoError(t, tool.store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.5,
		Content:    promptEcho,
		Tags:       "session-message",
	}))
	require.NoError(t, tool.store.StoreRecall(ctx, &memory.RecallItem{
		AgentID:    "agent-1",
		SessionKey: "session-1",
		Role:       "assistant",
		Sector:     memory.SectorSemantic,
		Importance: 0.9,
		Content:    "Regression ledger entry: xyzzy_nonexistent_topic_42 was intentionally left undefined.",
		Tags:       "semantic-fact",
	}))
	require.NoError(t, tool.store.SetWorkingContext(ctx, "agent-1", "session-1", promptEcho))

	resp := executeAndParse(t, tool, `{"action":"search","query":"xyzzy_nonexistent_topic_42","limit":5}`)
	assert.True(t, resp.Success)
	require.NotEmpty(t, resp.Results)
	assert.Contains(t, resp.Results[0].Content, "intentionally left undefined")
	for _, entry := range resp.Results {
		assert.False(t, strings.Contains(strings.ToLower(entry.Content), "search your memory"))
	}
}

func TestMemoryTool_Update(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	// Write
	writeResp := executeAndParse(t, tool, `{"action":"write","content":"Initial content."}`)
	id := writeResp.Message[len("Stored in recall tier with ID: "):]

	// Update
	updateResp := executeAndParse(t, tool, `{"action":"update","id":"`+id+`","content":"Updated content with more detail."}`)
	assert.True(t, updateResp.Success)

	// Verify
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	require.Len(t, readResp.Results, 1)
	assert.Contains(t, readResp.Results[0].Content, "Updated content")
}

func TestMemoryTool_Delete(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	// Write
	writeResp := executeAndParse(t, tool, `{"action":"write","content":"Content to delete."}`)
	id := writeResp.Message[len("Stored in recall tier with ID: "):]

	// Delete
	delResp := executeAndParse(t, tool, `{"action":"delete","id":"`+id+`"}`)
	assert.True(t, delResp.Success)

	// Verify deleted
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	assert.False(t, readResp.Success)
	assert.Contains(t, readResp.Message, "not found")
}

func TestMemoryTool_Status(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"status"}`)
	assert.True(t, resp.Success)
	require.NotNil(t, resp.Status)
	assert.Empty(t, cmp.Diff("normal", resp.Status.PressureLevel))
	assert.Empty(t, cmp.Diff(0, resp.Status.RecallItemCount))
}

func TestMemoryTool_InvalidAction(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"explode"}`)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "unknown action")
}

func TestMemoryTool_InvalidJSON(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `not json`)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "invalid input")
}

func TestMemoryTool_MissingRequiredFields(t *testing.T) {
	t.Parallel()
	tool := newTestMemoryTool(t)

	tests := []struct {
		name  string
		input string
		msg   string
	}{
		{"search no query", `{"action":"search"}`, "query is required"},
		{"read no id", `{"action":"read"}`, "id is required"},
		{"write no content", `{"action":"write"}`, "content is required"},
		{"update no id", `{"action":"update","content":"x"}`, "id and content are required"},
		{"update no content", `{"action":"update","id":"x"}`, "id and content are required"},
		{"delete no id", `{"action":"delete"}`, "id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := executeAndParse(t, tool, tt.input)
			assert.False(t, resp.Success)
			assert.Contains(t, resp.Message, tt.msg)
		})
	}
}
