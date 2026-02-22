package tools

import (
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/stretchr/testify/require"
)

func TestFocusHistoryTool_NoHistory(t *testing.T) {
	t.Parallel()
	delegate := newMockFocusDelegate()
	tool := NewFocusHistoryTool(delegate, func() string { return "session-no-history" })

	result := tool.Execute(t.Context(), map[string]interface{}{})
	require.Contains(t, result.ForLLM, "No completed focus history found")
}

func TestFocusHistoryTool_NoSession(t *testing.T) {
	t.Parallel()
	tool := NewFocusHistoryTool(newMockFocusDelegate(), func() string { return "" })
	result := tool.Execute(t.Context(), map[string]interface{}{})
	require.Contains(t, result.ForLLM, "no active session")
}

func TestFocusHistoryTool_FiltersByQueryAndOrdersByCompletion(t *testing.T) {
	t.Parallel()
	delegate := newMockFocusDelegate()
	ctx := t.Context()
	sk := "session-1"
	now := time.Now()
	block := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{
				Topic:       "reduce API latency",
				Goal:        "improve api latency",
				CompletedAt: now.Add(-2 * time.Hour),
				Summary:     "Added cache",
				Outcome:     "done",
			},
			{
				Topic:       "fix auth regression",
				Goal:        "fix auth regression",
				CompletedAt: now.Add(-1 * time.Hour),
				Summary:     "Patched token parsing",
				Outcome:     "resolved",
			},
		},
	}
	raw, err := jsonv2.Marshal(block)
	require.NoError(t, err)
	err = delegate.UpsertKV(ctx, focusAgentID, knowledgeKVPrefix+sk, string(raw))
	require.NoError(t, err)

	tool := NewFocusHistoryTool(delegate, func() string { return sk })
	result := tool.Execute(ctx, map[string]interface{}{"query": "auth", "limit": 10.0})
	require.NotNil(t, result)
	require.NotNil(t, result)

	var response struct {
		Items []KnowledgeEntry `json:"items"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &response))
	require.Len(t, response.Items, 1)
	require.Equal(t, "fix auth regression", response.Items[0].Topic)
}

func TestFocusHistoryTool_InvalidLimit(t *testing.T) {
	t.Parallel()
	delegate := newMockFocusDelegate()
	sk := "session-2"
	block := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{
				Topic:       "first focus",
				CompletedAt: time.Now(),
				Summary:     "seed",
			},
		},
	}
	raw, err := jsonv2.Marshal(block)
	require.NoError(t, err)
	err = delegate.UpsertKV(t.Context(), focusAgentID, knowledgeKVPrefix+sk, string(raw))
	require.NoError(t, err)

	tool := NewFocusHistoryTool(delegate, func() string { return sk })
	result := tool.Execute(t.Context(), map[string]interface{}{"limit": "x"})
	require.Contains(t, result.ForLLM, "invalid limit")
}

func TestFocusHistoryTool_IsolatesSessionData(t *testing.T) {
	t.Parallel()
	delegate := newMockFocusDelegate()
	ctx := t.Context()

	skA := "session-a"
	skB := "session-b"
	blockA := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{Topic: "session a item", CompletedAt: time.Now(), Summary: "A"},
		},
	}
	blockB := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{Topic: "session b item", CompletedAt: time.Now().Add(10 * time.Second), Summary: "B"},
		},
	}
	rawA, err := jsonv2.Marshal(blockA)
	require.NoError(t, err)
	rawB, err := jsonv2.Marshal(blockB)
	require.NoError(t, err)
	require.NoError(t, delegate.UpsertKV(ctx, focusAgentID, knowledgeKVPrefix+skA, string(rawA)))
	require.NoError(t, delegate.UpsertKV(ctx, focusAgentID, knowledgeKVPrefix+skB, string(rawB)))

	tool := NewFocusHistoryTool(delegate, func() string { return skB })
	result := tool.Execute(ctx, map[string]interface{}{})

	var response struct {
		Items []KnowledgeEntry `json:"items"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &response))
	require.Len(t, response.Items, 1)
	require.Equal(t, "session b item", response.Items[0].Topic)
}

func TestFocusHistoryTool_SortsByCompletedAtDescending(t *testing.T) {
	t.Parallel()
	delegate := newMockFocusDelegate()
	ctx := t.Context()
	sk := "session-sort"

	oldest := time.Now().Add(-2 * time.Hour)
	newest := time.Now()
	middle := time.Now().Add(-1 * time.Hour)

	block := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{Topic: "old", CompletedAt: oldest},
			{Topic: "new", CompletedAt: newest},
			{Topic: "mid", CompletedAt: middle},
		},
	}
	raw, err := jsonv2.Marshal(block)
	require.NoError(t, err)
	require.NoError(t, delegate.UpsertKV(ctx, focusAgentID, knowledgeKVPrefix+sk, string(raw)))

	tool := NewFocusHistoryTool(delegate, func() string { return sk })
	result := tool.Execute(ctx, map[string]interface{}{"limit": 3.0})

	var response struct {
		Items []KnowledgeEntry `json:"items"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &response))
	require.Len(t, response.Items, 3)
	require.Equal(t, "new", response.Items[0].Topic)
	require.Equal(t, "mid", response.Items[1].Topic)
	require.Equal(t, "old", response.Items[2].Topic)
}
