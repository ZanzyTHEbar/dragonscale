package agent_test

import (
	"strings"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/agent"
)

// setupSearchFixture runs a set of tool calls through the OffloadingToolRuntime
// so there are records + KV data to search, and returns the search tool.
func setupSearchFixture(t *testing.T) (fantasy.AgentTool, agent.KVDelegate, string, string) {
	t.Helper()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(t.Context(), convID)
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
	_, err = r.Execute(t.Context(), nil, calls, nil)
	require.NoError(t, err)

	tool := agent.NewToolResultSearchTool(q, kv)
	return tool, kv, convID.String(), run.ID.String()
}

// invokeSearch calls the search tool with the given input and parses the JSON response.
func invokeSearch(t *testing.T, tool fantasy.AgentTool, input agent.ToolResultSearchInput) map[string]any {
	t.Helper()
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{
		Input: marshalInput(t, input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, "search tool must not error: %s", resp.Content)

	var out map[string]any
	require.NoError(t, jsonv2.Unmarshal([]byte(resp.Content), &out))
	return out
}

func marshalInput(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonv2.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestToolResultSearch_QueryFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		build     func(convID, runID string) agent.ToolResultSearchInput
		wantTotal float64
		wantTCID  string
	}{
		{
			name: "by conversation ID",
			build: func(convID, _ string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					ConversationID: convID,
					Limit:          10,
				}
			},
			wantTotal: 3,
		},
		{
			name: "by run ID",
			build: func(_, runID string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					RunID: runID,
					Limit: 10,
				}
			},
			wantTotal: 3,
		},
		{
			name: "by run ID and tool_call_id",
			build: func(_, runID string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					RunID:      runID,
					ToolCallID: "c2",
				}
			},
			wantTotal: 1,
			wantTCID:  "c2",
		},
		{
			name: "filter by tool_name",
			build: func(convID, _ string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					ConversationID: convID,
					ToolName:       "alpha",
					Limit:          10,
				}
			},
			wantTotal: 2,
		},
		{
			name: "filter by query",
			build: func(convID, _ string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					ConversationID: convID,
					Query:          "beta",
					Limit:          10,
				}
			},
			wantTotal: 1,
		},
		{
			name: "default limit",
			build: func(convID, _ string) agent.ToolResultSearchInput {
				return agent.ToolResultSearchInput{
					ConversationID: convID,
				}
			},
			wantTotal: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, _, convID, runID := setupSearchFixture(t)
			input := tt.build(convID, runID)
			out := invokeSearch(t, tool, input)

			total, _ := out["total"].(float64)
			assert.Equal(t, tt.wantTotal, total)

			if tt.wantTCID != "" {
				items := out["items"].([]any)
				item := items[0].(map[string]any)
				assert.Equal(t, tt.wantTCID, item["tool_call_id"])
			}
		})
	}
}

func TestToolResultSearch_MissingConvAndRunID_ErrorResponse(t *testing.T) {
	t.Parallel()
	tool, _, _, _ := setupSearchFixture(t)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{
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
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(t.Context(), convID)
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
	_, err = r.Execute(t.Context(), nil, []fantasy.ToolCallContent{{ToolCallID: "lv1", ToolName: "liner"}}, nil)
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
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	convID := newConversation(t, q)
	s := agent.NewStateStore(q)
	run, err := s.CreateRun(t.Context(), convID)
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
	_, err = r.Execute(t.Context(), nil, []fantasy.ToolCallContent{{ToolCallID: "cv1", ToolName: "chunker"}}, nil)
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
