package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/messages"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockKVStore struct {
	data map[string]string
}

func newMockKV() *mockKVStore {
	return &mockKVStore{data: make(map[string]string)}
}

func (m *mockKVStore) GetKV(_ context.Context, agentID, key string) (string, error) {
	return m.data[agentID+":"+key], nil
}

func (m *mockKVStore) UpsertKV(_ context.Context, agentID, key, value string) error {
	m.data[agentID+":"+key] = value
	return nil
}

func (m *mockKVStore) DeleteKV(_ context.Context, agentID, key string) error {
	delete(m.data, agentID+":"+key)
	return nil
}

type mockFocusDelegate struct {
	kv *mockKVStore
}

func newMockFocusDelegate() *mockFocusDelegate {
	return &mockFocusDelegate{kv: newMockKV()}
}

func (m *mockFocusDelegate) GetKV(ctx context.Context, agentID, key string) (string, error) {
	return m.kv.GetKV(ctx, agentID, key)
}
func (m *mockFocusDelegate) UpsertKV(ctx context.Context, agentID, key, value string) error {
	return m.kv.UpsertKV(ctx, agentID, key, value)
}
func (m *mockFocusDelegate) DeleteKV(ctx context.Context, agentID, key string) error {
	return m.kv.DeleteKV(ctx, agentID, key)
}

func TestStartFocus(t *testing.T) {
	sm := session.NewSessionManager("")
	sk := "test-session"
	sm.GetOrCreate(sk)

	sm.AddMessage(sk, "user", "hello")
	sm.AddMessage(sk, "assistant", "hi there")

	delegate := newMockFocusDelegate()
	tool := NewStartFocusTool(delegate, sm, func() string { return sk })

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"topic": "investigate auth bug",
	})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "Focus started on: investigate auth bug")
	assert.Contains(t, result.ForLLM, "Checkpoint at message 2")

	raw, _ := delegate.GetKV(ctx, focusAgentID, focusKVPrefix+sk)
	require.NotEmpty(t, raw)

	var state FocusState
	require.NoError(t, json.Unmarshal([]byte(raw), &state))
	assert.Equal(t, "investigate auth bug", state.Topic)
	assert.Equal(t, 2, state.CheckpointIndex)
}

func TestStartFocus_MissingTopic(t *testing.T) {
	sm := session.NewSessionManager("")
	delegate := newMockFocusDelegate()
	tool := NewStartFocusTool(delegate, sm, func() string { return "s" })

	result := tool.Execute(context.Background(), map[string]interface{}{})
	assert.Contains(t, result.ForLLM, "topic is required")
}

func TestCompleteFocus(t *testing.T) {
	sm := session.NewSessionManager("")
	sk := "test-session"
	sm.GetOrCreate(sk)

	sm.AddMessage(sk, "user", "hello")
	sm.AddMessage(sk, "assistant", "hi")

	delegate := newMockFocusDelegate()

	startTool := NewStartFocusTool(delegate, sm, func() string { return sk })
	ctx := context.Background()
	startTool.Execute(ctx, map[string]interface{}{"topic": "debug auth"})

	sm.AddMessage(sk, "user", "check logs")
	sm.AddMessage(sk, "assistant", "found the issue in auth.go")
	sm.AddMessage(sk, "user", "fix it")
	sm.AddMessage(sk, "assistant", "done, applied patch")
	sm.AddMessage(sk, "user", "test it")
	sm.AddMessage(sk, "assistant", "all tests pass")

	historyBefore := sm.GetHistory(sk)
	require.Equal(t, 8, len(historyBefore))

	completeTool := NewCompleteFocusTool(delegate, sm, func() string { return sk })
	result := completeTool.Execute(ctx, map[string]interface{}{
		"summary": "Found auth bug in token validation. Fixed by adding expiry check.",
	})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "Focus completed")
	assert.Contains(t, result.ForLLM, "debug auth")

	historyAfter := sm.GetHistory(sk)
	assert.Less(t, len(historyAfter), len(historyBefore))

	// Pre-checkpoint messages should be preserved
	assert.Equal(t, "hello", historyAfter[0].Content)
	assert.Equal(t, "hi", historyAfter[1].Content)

	// Knowledge should be persisted
	knowledgeRaw, _ := delegate.GetKV(ctx, focusAgentID, knowledgeKVPrefix+sk)
	require.NotEmpty(t, knowledgeRaw)

	var kb KnowledgeBlock
	require.NoError(t, json.Unmarshal([]byte(knowledgeRaw), &kb))
	require.Len(t, kb.Entries, 1)
	assert.Equal(t, "debug auth", kb.Entries[0].Topic)
	assert.Contains(t, kb.Entries[0].Summary, "token validation")

	// Focus state should be cleaned up
	focusRaw, _ := delegate.GetKV(ctx, focusAgentID, focusKVPrefix+sk)
	assert.Empty(t, focusRaw)
}

func TestCompleteFocus_NoActiveFocus(t *testing.T) {
	sm := session.NewSessionManager("")
	sk := "test-session"
	sm.GetOrCreate(sk)

	delegate := newMockFocusDelegate()
	tool := NewCompleteFocusTool(delegate, sm, func() string { return sk })

	result := tool.Execute(context.Background(), map[string]interface{}{
		"summary": "some summary",
	})
	assert.Contains(t, result.ForLLM, "no active focus session")
}

func TestCompleteFocus_MultipleKnowledgeEntries(t *testing.T) {
	sm := session.NewSessionManager("")
	sk := "test-session"
	sm.GetOrCreate(sk)
	delegate := newMockFocusDelegate()
	ctx := context.Background()

	startTool := NewStartFocusTool(delegate, sm, func() string { return sk })
	completeTool := NewCompleteFocusTool(delegate, sm, func() string { return sk })

	// First focus cycle
	sm.AddMessage(sk, "user", "start")
	startTool.Execute(ctx, map[string]interface{}{"topic": "topic A"})
	sm.AddMessage(sk, "user", "work A")
	sm.AddMessage(sk, "assistant", "result A")
	completeTool.Execute(ctx, map[string]interface{}{"summary": "learned A"})

	// Second focus cycle
	sm.AddMessage(sk, "user", "more work")
	startTool.Execute(ctx, map[string]interface{}{"topic": "topic B"})
	sm.AddMessage(sk, "user", "work B")
	sm.AddMessage(sk, "assistant", "result B")
	completeTool.Execute(ctx, map[string]interface{}{"summary": "learned B"})

	knowledgeRaw, _ := delegate.GetKV(ctx, focusAgentID, knowledgeKVPrefix+sk)
	var kb KnowledgeBlock
	require.NoError(t, json.Unmarshal([]byte(knowledgeRaw), &kb))
	require.Len(t, kb.Entries, 2)
	assert.Equal(t, "topic A", kb.Entries[0].Topic)
	assert.Equal(t, "topic B", kb.Entries[1].Topic)
}

func TestPruneHistory(t *testing.T) {
	tool := &CompleteFocusTool{}

	tests := []struct {
		name          string
		history       []messages.Message
		checkpointIdx int
		wantMinLen    int
		wantMaxLen    int
	}{
		{
			name: "prune investigation messages",
			history: []messages.Message{
				{Role: "user", Content: "pre-1"},
				{Role: "assistant", Content: "pre-2"},
				{Role: "user", Content: "investigate-1"},
				{Role: "assistant", Content: "investigate-2"},
				{Role: "user", Content: "investigate-3"},
				{Role: "assistant", Content: "investigate-4"},
				{Role: "user", Content: "investigate-5"},
				{Role: "assistant", Content: "investigate-6"},
			},
			checkpointIdx: 2,
			wantMinLen:    2 + 4, // pre-checkpoint + tail
			wantMaxLen:    2 + 4,
		},
		{
			name: "checkpoint at end does nothing",
			history: []messages.Message{
				{Role: "user", Content: "msg1"},
				{Role: "assistant", Content: "msg2"},
			},
			checkpointIdx: 2,
			wantMinLen:    2,
			wantMaxLen:    2,
		},
		{
			name: "short investigation keeps all",
			history: []messages.Message{
				{Role: "user", Content: "pre-1"},
				{Role: "assistant", Content: "pre-2"},
				{Role: "user", Content: "inv-1"},
				{Role: "assistant", Content: "inv-2"},
			},
			checkpointIdx: 2,
			wantMinLen:    4,
			wantMaxLen:    4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.pruneHistory(tt.history, tt.checkpointIdx)
			assert.GreaterOrEqual(t, len(result), tt.wantMinLen)
			assert.LessOrEqual(t, len(result), tt.wantMaxLen)

			// Pre-checkpoint messages should always be preserved
			for i := 0; i < tt.checkpointIdx && i < len(result); i++ {
				assert.Equal(t, tt.history[i].Content, result[i].Content)
			}
		})
	}
}

func TestKnowledgeBlockFormat(t *testing.T) {
	kb := &KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{Topic: "Auth Flow", Summary: "Token validation needs expiry check."},
			{Topic: "Caching", Summary: "Redis preferred over in-memory for persistence."},
		},
	}

	formatted := kb.FormatBlock()
	assert.Contains(t, formatted, "# Knowledge")
	assert.Contains(t, formatted, "## Auth Flow")
	assert.Contains(t, formatted, "Token validation needs expiry check.")
	assert.Contains(t, formatted, "## Caching")
}

func TestKnowledgeBlockFormat_Empty(t *testing.T) {
	kb := &KnowledgeBlock{}
	assert.Empty(t, kb.FormatBlock())
}

func TestLoadKnowledgeBlock(t *testing.T) {
	delegate := newMockFocusDelegate()
	ctx := context.Background()

	block := LoadKnowledgeBlock(ctx, delegate, "nonexistent")
	assert.Empty(t, block)

	kb := KnowledgeBlock{
		Entries: []KnowledgeEntry{
			{Topic: "Test", Summary: "Test summary"},
		},
	}
	data, _ := json.Marshal(kb)
	_ = delegate.UpsertKV(ctx, focusAgentID, knowledgeKVPrefix+"test-session", string(data))

	block = LoadKnowledgeBlock(ctx, delegate, "test-session")
	assert.Contains(t, block, "# Knowledge")
	assert.Contains(t, block, "## Test")
	assert.Contains(t, block, "Test summary")
}
