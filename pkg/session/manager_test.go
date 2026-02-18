package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/messages"
)

func TestSanitizeFilename(t *testing.T) {
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

func TestAddFullMessage_HardCap(t *testing.T) {
	sm := NewSessionManager("")

	key := "test-hard-cap"

	// Add 201 messages to trigger the hard cap
	for i := 0; i < 201; i++ {
		sm.AddFullMessage(key, messages.Message{Role: "user", Content: "msg"})
	}

	history := sm.GetHistory(key)
	if len(history) > 200 {
		t.Errorf("expected <= 200 messages after hard cap, got %d", len(history))
	}
	if len(history) != 50 {
		t.Errorf("expected 50 messages after hard cap truncation, got %d", len(history))
	}
}

func TestCleanupStale(t *testing.T) {
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
