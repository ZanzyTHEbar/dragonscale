package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
)

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"telegram:123456", "telegram_123456"},
		{"discord:987654321", "discord_987654321"},
		{"slack:C01234", "slack_C01234"},
		{"no-colons-here", "no-colons-here"},
		{"multiple:colons:here", "multiple_colons_here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSave_WithColonInKey(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Create a session with a key containing colon (typical channel session key).
	key := "telegram:123456"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	// Save should succeed even though the key contains ':'
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// The file on disk should use sanitized name.
	expectedFile := filepath.Join(tmpDir, "telegram_123456.json")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("expected session file %s to exist", expectedFile)
	}

	// Load into a fresh manager and verify the session round-trips.
	sm2 := NewSessionManager(tmpDir)
	history := sm2.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected message content %q, got %q", "hello", history[0].Content)
	}
}

func TestSave_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	badKeys := []string{"", ".", "..", "foo/bar", "foo\\bar"}
	for _, key := range badKeys {
		sm.GetOrCreate(key)
		if err := sm.Save(key); err == nil {
			t.Errorf("Save(%q) should have failed but didn't", key)
		}
	}
}

func TestTruncateHistory_ToolCallAware(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager("") // in-memory only

	key := "test-tool-truncation"

	// Build a history: [user, assistant+tool_calls, tool, tool, assistant]
	sm.AddFullMessage(key, messages.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, messages.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []messages.ToolCall{
			{ID: "call_1", Function: &messages.FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`}},
			{ID: "call_2", Function: &messages.FunctionCall{Name: "read", Arguments: `{"path":"foo"}`}},
		},
	})
	sm.AddFullMessage(key, messages.Message{Role: "tool", Content: "file1\nfile2", ToolCallID: "call_1"})
	sm.AddFullMessage(key, messages.Message{Role: "tool", Content: "file contents", ToolCallID: "call_2"})
	sm.AddFullMessage(key, messages.Message{Role: "assistant", Content: "I found the files"})

	// Truncate to keep last 2 messages (assistant response + one tool result).
	// The tool-call-aware logic should expand to include the full tool call pair.
	sm.TruncateHistory(key, 2)

	history := sm.GetHistory(key)

	// Verify: first remaining message should NOT be role "tool".
	// It should be the assistant message with tool_calls.
	if len(history) == 0 {
		t.Fatal("expected non-empty history after truncation")
	}

	if history[0].Role == "tool" {
		t.Errorf("first message after truncation is 'tool' -- tool-call pair was split")
	}

	if history[0].Role != "assistant" {
		t.Errorf("expected first message to be 'assistant', got %q", history[0].Role)
	}

	// Should have: assistant+tool_calls, tool, tool, assistant = 4 messages
	if len(history) != 4 {
		t.Errorf("expected 4 messages (full tool-call group), got %d", len(history))
	}
}

func TestTruncateHistory_NoToolCalls(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager("")

	key := "test-no-tools"

	sm.AddFullMessage(key, messages.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, messages.Message{Role: "assistant", Content: "hi"})
	sm.AddFullMessage(key, messages.Message{Role: "user", Content: "how are you"})
	sm.AddFullMessage(key, messages.Message{Role: "assistant", Content: "fine"})

	sm.TruncateHistory(key, 2)

	history := sm.GetHistory(key)
	if len(history) != 2 {
		t.Errorf("expected 2 messages, got %d", len(history))
	}

	if history[0].Role != "user" {
		t.Errorf("expected first message 'user', got %q", history[0].Role)
	}
}

func TestAddFullMessage_NoHardCapTruncation(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager("")

	key := "test-hard-cap"

	// Add many messages and verify startup continuity is not lossy.
	for i := 0; i < 201; i++ {
		sm.AddFullMessage(key, messages.Message{Role: "user", Content: "msg"})
	}

	history := sm.GetHistory(key)
	if len(history) != 201 {
		t.Errorf("expected 201 messages without hard-cap truncation, got %d", len(history))
	}
}

func TestCleanupStale(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager("")

	sm.AddFullMessage("active", messages.Message{Role: "user", Content: "hi"})
	sm.AddFullMessage("stale", messages.Message{Role: "user", Content: "old"})

	// Make "stale" session appear old by directly modifying its Updated time
	sm.mu.Lock()
	sm.sessions["stale"].Updated = sm.sessions["stale"].Updated.Add(-8 * 24 * time.Hour) // 8 days ago
	sm.mu.Unlock()

	removed := sm.CleanupStale(7 * 24 * time.Hour) // 7 day TTL

	if removed != 1 {
		t.Errorf("expected 1 stale session removed, got %d", removed)
	}

	history := sm.GetHistory("stale")
	if len(history) != 0 {
		t.Errorf("expected stale session to be removed")
	}

	history = sm.GetHistory("active")
	if len(history) != 1 {
		t.Errorf("expected active session to remain")
	}
}

func TestSessionManager_DelegatePersistence(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	if err != nil {
		t.Fatalf("NewLibSQLInMemory: %v", err)
	}
	if err := del.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer del.Close()

	sm := NewSessionManager("", WithSessionDelegate(del, "test-agent"))

	key := "delegate-session"
	sm.AddMessage(key, "user", "hello from delegate")
	sm.AddMessage(key, "assistant", "hi back")

	history := sm.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in-memory, got %d", len(history))
	}

	items, err := del.ListRecallItems(t.Context(), "test-agent", key, 100, 0)
	if err != nil {
		t.Fatalf("ListRecallItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 recall items in DB, got %d", len(items))
	}
	if items[0].Content != "hello from delegate" {
		t.Errorf("expected first item content 'hello from delegate', got %q", items[0].Content)
	}
}

func TestSessionManager_DelegateSaveIsNoop(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	if err != nil {
		t.Fatalf("NewLibSQLInMemory: %v", err)
	}
	if err := del.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer del.Close()

	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir, WithSessionDelegate(del, "test-agent"))

	key := "telegram:999"
	sm.AddMessage(key, "user", "test")

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			t.Errorf("delegate mode should not write JSON files, found %s", e.Name())
		}
	}
}

func TestSessionManager_DelegateBootstrapPaginationAndOrder(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	if err != nil {
		t.Fatalf("NewLibSQLInMemory: %v", err)
	}
	if err := del.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer del.Close()

	const (
		sessionKey = "bootstrap-order"
		totalMsgs  = 1200
	)

	writer := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	for i := 0; i < totalMsgs; i++ {
		writer.AddMessage(sessionKey, "user", fmt.Sprintf("m-%04d", i))
	}

	// New manager instance exercises delegate bootstrap restore path.
	reader := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	history := reader.GetHistory(sessionKey)

	if len(history) != totalMsgs {
		t.Fatalf("expected %d restored messages, got %d", totalMsgs, len(history))
	}
	if history[0].Content != "m-0000" {
		t.Fatalf("expected oldest message first, got %q", history[0].Content)
	}
	if history[len(history)-1].Content != "m-1199" {
		t.Fatalf("expected newest message last, got %q", history[len(history)-1].Content)
	}
}

func TestSessionManager_ProjectionPointerPersistedAndRestored(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(t.Context()))
	defer del.Close()

	sessionKey := "ptr-persist"
	sm := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	sm.AddMessage(sessionKey, "user", "first")
	sm.AddMessage(sessionKey, "assistant", "second")
	sm.AddMessage(sessionKey, "user", "third")

	// Read persisted pointer from KV
	raw, err := del.GetKV(t.Context(), "test-agent", projectionPointerKey(sessionKey))
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var ptr ProjectionPointer
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &ptr))
	assert.NotZero(t, ptr.FirstMessageID)
	assert.NotZero(t, ptr.LastMessageID)
	assert.False(t, ptr.FirstCreatedAt.IsZero())
	assert.False(t, ptr.LastCreatedAt.IsZero())
	assert.Empty(t, cmp.Diff(3, ptr.Count))

	// New manager restores; pointer is re-persisted (same values)
	sm2 := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	history := sm2.GetHistory(sessionKey)
	require.Len(t, history, 3)
	raw2, err := del.GetKV(t.Context(), "test-agent", projectionPointerKey(sessionKey))
	require.NoError(t, err)
	require.NotEmpty(t, raw2)
	var ptr2 ProjectionPointer
	require.NoError(t, jsonv2.Unmarshal([]byte(raw2), &ptr2))
	assert.Empty(t, cmp.Diff(ptr, ptr2, cmpopts.IgnoreFields(ProjectionPointer{}, "FirstCreatedAt", "LastCreatedAt")))
}

func TestSessionManager_ProjectionPointerUpdatedOnAppend(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(t.Context()))
	defer del.Close()

	sessionKey := "ptr-append"
	sm := NewSessionManager("", WithSessionDelegate(del, "test-agent"))

	for i := 0; i < 4; i++ {
		sm.AddMessage(sessionKey, "user", fmt.Sprintf("msg-%d", i))
		raw, err := del.GetKV(t.Context(), "test-agent", projectionPointerKey(sessionKey))
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		var ptr ProjectionPointer
		require.NoError(t, jsonv2.Unmarshal([]byte(raw), &ptr))
		assert.Empty(t, cmp.Diff(i+1, ptr.Count))
	}
}

func TestSessionManager_IntegrityMismatchRestoreStillSucceeds(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(t.Context()))
	defer del.Close()

	sessionKey := "integrity-mismatch"
	sm := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	sm.AddMessage(sessionKey, "user", "a")
	sm.AddMessage(sessionKey, "assistant", "b")

	// Corrupt the stored pointer to simulate prior state mismatch
	corrupt := ProjectionPointer{Count: 0}
	data, _ := jsonv2.Marshal(corrupt)
	require.NoError(t, del.UpsertKV(t.Context(), "test-agent", projectionPointerKey(sessionKey), string(data)))

	// New manager restores; should succeed (lossless) despite mismatch
	sm2 := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	history := sm2.GetHistory(sessionKey)
	require.Len(t, history, 2)
	assert.Empty(t, cmp.Diff("a", history[0].Content))
	assert.Empty(t, cmp.Diff("b", history[1].Content))

	// Pointer should now reflect restored state
	raw, err := del.GetKV(t.Context(), "test-agent", projectionPointerKey(sessionKey))
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var ptr ProjectionPointer
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &ptr))
	assert.Empty(t, cmp.Diff(2, ptr.Count))
}

func TestSessionManager_ProjectionBackfillStatusPersistedOnBootstrap(t *testing.T) {
	t.Parallel()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(t.Context()))
	defer del.Close()

	writer := NewSessionManager("", WithSessionDelegate(del, "test-agent"))
	writer.AddMessage("session-a", "user", "hello")
	writer.AddMessage("session-a", "assistant", "hi")
	writer.AddMessage("session-b", "user", "task")

	_ = NewSessionManager("", WithSessionDelegate(del, "test-agent"))

	raw, err := del.GetKV(t.Context(), "test-agent", projectionBackfillStatusKey())
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var status ProjectionBackfillStatus
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &status))
	assert.Empty(t, cmp.Diff(1, status.Version))
	assert.GreaterOrEqual(t, status.SessionsScanned, 2)
	assert.GreaterOrEqual(t, status.PointersUpdated, 0)
	assert.WithinDuration(t, time.Now().UTC(), status.CompletedAt, 5*time.Second)
}
