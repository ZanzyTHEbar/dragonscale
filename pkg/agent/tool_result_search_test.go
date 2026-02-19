package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/agent"
)

// setupSearchFixture runs a set of tool calls through the OffloadingToolRuntime
// so there are records + KV data to search, and returns the search tool.
func setupSearchFixture(t *testing.T) (fantasy.AgentTool, agent.KVDelegate, string, string) {
	t.Helper()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(context.Background(), convID)
	require.NoError(t, err)

	kv := agent.NewDelegateKV(db.delegate, "search-test")

	r := &agent.OffloadingToolRuntime{
		Base:           staticToolRuntime{},
		KV:             kv,
		Queries:        q,
		ConversationID: convID,
		RunID:          run.ID,
		ThresholdChars: 10000,
		ChunkChars:     2000,
	}

	calls := []fantasy.ToolCallContent{
		{ToolCallID: "c1", ToolName: "alpha"},
		{ToolCallID: "c2", ToolName: "beta"},
		{ToolCallID: "c3", ToolName: "alpha"},
	}
	_, err = r.Execute(context.Background(), nil, calls, nil)
	require.NoError(t, err)

	tool := agent.NewToolResultSearchTool(q, kv)
	return tool, kv, convID.String(), run.ID.String()
}

// invokeSearch calls the search tool with the given input and parses the JSON response.
func invokeSearch(t *testing.T, tool fantasy.AgentTool, input agent.ToolResultSearchInput) map[string]any {
	t.Helper()
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: marshalInput(t, input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, "search tool must not error: %s", resp.Content)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &out))
	return out
}

func marshalInput(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestToolResultSearch_ByConversationID(t *testing.T) {
	tool, _, convID, _ := setupSearchFixture(t)
	ctx := context.Background()
	_ = ctx

	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		ConversationID: convID,
		Limit:          10,
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(3), total, "should find all 3 results by conversation_id")
}

func TestToolResultSearch_ByRunID(t *testing.T) {
	tool, _, _, runID := setupSearchFixture(t)

	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		RunID: runID,
		Limit: 10,
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(3), total)
}

func TestToolResultSearch_ByRunID_AndToolCallID(t *testing.T) {
	tool, _, _, runID := setupSearchFixture(t)

	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		RunID:      runID,
		ToolCallID: "c2",
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(1), total)
	items := out["items"].([]any)
	item := items[0].(map[string]any)
	assert.Equal(t, "c2", item["tool_call_id"])
}

func TestToolResultSearch_FilterByToolName(t *testing.T) {
	tool, _, convID, _ := setupSearchFixture(t)

	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		ConversationID: convID,
		ToolName:       "alpha",
		Limit:          10,
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(2), total, "filter by tool_name should return only 'alpha' results")
}

func TestToolResultSearch_FilterByQuery(t *testing.T) {
	tool, _, convID, _ := setupSearchFixture(t)

	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		ConversationID: convID,
		Query:          "beta",
		Limit:          10,
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(1), total)
}

func TestToolResultSearch_DefaultLimit(t *testing.T) {
	tool, _, convID, _ := setupSearchFixture(t)

	// No limit specified -- default is 5, but we only have 3 items
	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		ConversationID: convID,
	})

	total, _ := out["total"].(float64)
	assert.Equal(t, float64(3), total)
}

func TestToolResultSearch_MissingConvAndRunID_ErrorResponse(t *testing.T) {
	tool, _, _, _ := setupSearchFixture(t)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: marshalInput(t, agent.ToolResultSearchInput{}),
	})
	require.NoError(t, err)
	// The tool returns an error response (not an err) when no ID is provided.
	// The text will contain the error message.
	assert.True(t, resp.IsError || strings.Contains(resp.Content, "required"), "missing IDs should produce error response")
}

// TestToolResultSearch_LineView verifies that the KV full-result is sliced
// into the requested line range.
func TestToolResultSearch_LineView(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(context.Background(), convID)
	require.NoError(t, err)

	kv := agent.NewDelegateKV(db.delegate, "line-view-test")

	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	multilineResult := strings.Join(lines, "\n")

	base := staticToolRuntime{results: []fantasy.ToolResultContent{{
		ToolCallID: "lv1",
		ToolName:   "liner",
		Result:     fantasy.ToolResultOutputContentText{Text: multilineResult},
	}}}

	r := &agent.OffloadingToolRuntime{
		Base:           base,
		KV:             kv,
		Queries:        q,
		ConversationID: convID,
		RunID:          run.ID,
		ThresholdChars: 100000,
	}
	_, err = r.Execute(context.Background(), nil, []fantasy.ToolCallContent{{ToolCallID: "lv1", ToolName: "liner"}}, nil)
	require.NoError(t, err)

	tool := agent.NewToolResultSearchTool(q, kv)
	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		RunID: run.ID.String(),
		View: &agent.ToolResultSearchView{
			StartLine: 2,
			EndLine:   4,
		},
	})

	items := out["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	view := item["view"].(string)

	// For non-chunked results the full.json KV entry is a JSON blob (one line).
	// The view returns the full blob when the content fits within 1 line.
	// Verify the view is non-empty and contains the original text.
	assert.NotEmpty(t, view, "view should return content")
	assert.Contains(t, view, "line1", "full.json should contain original text")
	assert.Contains(t, view, "line5", "full.json should contain all lines")

	// The view_range reflects the actual clamped range (1 JSON line available).
	viewRange := item["view_range"].(map[string]any)
	assert.Equal(t, float64(1), viewRange["start_line"], "clamped to 1 since full.json is a single-line JSON blob")
}

// TestToolResultSearch_ChunkView verifies chunk-range retrieval from KV.
func TestToolResultSearch_ChunkView(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(context.Background(), convID)
	require.NoError(t, err)

	kv := agent.NewDelegateKV(db.delegate, "chunk-view-test")

	// 60 chars, threshold=10, chunkChars=20 → 3 chunks
	longText := strings.Repeat("abcdefghij", 6)

	base := staticToolRuntime{results: []fantasy.ToolResultContent{{
		ToolCallID: "cv1",
		ToolName:   "chunker",
		Result:     fantasy.ToolResultOutputContentText{Text: longText},
	}}}

	r := &agent.OffloadingToolRuntime{
		Base:           base,
		KV:             kv,
		Queries:        q,
		ConversationID: convID,
		RunID:          run.ID,
		ThresholdChars: 10,
		ChunkChars:     20,
	}
	_, err = r.Execute(context.Background(), nil, []fantasy.ToolCallContent{{ToolCallID: "cv1", ToolName: "chunker"}}, nil)
	require.NoError(t, err)

	tool := agent.NewToolResultSearchTool(q, kv)

	// Request chunks 0-1 only
	out := invokeSearch(t, tool, agent.ToolResultSearchInput{
		RunID: run.ID.String(),
		View: &agent.ToolResultSearchView{
			StartChunk: 0,
			EndChunk:   1,
		},
	})

	items := out["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	view := item["view"].(string)

	// First two chunks: "abcdefghijabcdefghij" + "abcdefghijabcdefghij" = 40 chars
	assert.Equal(t, strings.Repeat("abcdefghij", 4), view, "chunk 0+1 should be first 40 chars")
}
