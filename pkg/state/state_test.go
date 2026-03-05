package state

import (
	"context"
	"testing"
)

// Tests for state manager with in-memory state (no delegate).
// Persistence is tested via integration tests with a real delegate.

func TestSetLastChannel(t *testing.T) {
	t.Parallel()

	sm := NewManager("/tmp/test")

	// Test SetLastChannel
	err := sm.SetLastChannel(context.Background(), "test-channel")
	// Without a delegate, persist returns nil but state is updated in memory
	if err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	// Verify the channel was saved in memory
	lastChannel := sm.GetLastChannel()
	if lastChannel != "test-channel" {
		t.Errorf("Expected channel 'test-channel', got '%s'", lastChannel)
	}

	// Verify timestamp was updated
	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}
}

func TestSetLastChatID(t *testing.T) {
	t.Parallel()

	sm := NewManager("/tmp/test")

	// Test SetLastChatID
	err := sm.SetLastChatID(context.Background(), "test-chat-id")
	if err != nil {
		t.Fatalf("SetLastChatID failed: %v", err)
	}

	// Verify the chat ID was saved in memory
	lastChatID := sm.GetLastChatID()
	if lastChatID != "test-chat-id" {
		t.Errorf("Expected chat ID 'test-chat-id', got '%s'", lastChatID)
	}

	// Verify timestamp was updated
	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}
}

func TestSetChannelAndChatID(t *testing.T) {
	t.Parallel()

	sm := NewManager("/tmp/test")

	// Test setting both channel and chat ID atomically
	err := sm.SetChannelAndChatID(context.Background(), "channel-1", "chat-1")
	if err != nil {
		t.Fatalf("SetChannelAndChatID failed: %v", err)
	}

	// Verify both were saved in memory
	if sm.GetLastChannel() != "channel-1" {
		t.Errorf("Expected channel 'channel-1', got '%s'", sm.GetLastChannel())
	}

	if sm.GetLastChatID() != "chat-1" {
		t.Errorf("Expected chat ID 'chat-1', got '%s'", sm.GetLastChatID())
	}
}

func TestNewManager_EmptyWorkspace(t *testing.T) {
	t.Parallel()

	sm := NewManager("/tmp/test")

	// Verify default state
	if sm.GetLastChannel() != "" {
		t.Errorf("Expected empty channel, got '%s'", sm.GetLastChannel())
	}

	if sm.GetLastChatID() != "" {
		t.Errorf("Expected empty chat ID, got '%s'", sm.GetLastChatID())
	}

	if !sm.GetTimestamp().IsZero() {
		t.Error("Expected zero timestamp for new state")
	}
}

func TestStateStruct(t *testing.T) {
	t.Parallel()

	// Test that State struct fields work correctly
	state := &State{
		LastChannel: "test-channel",
		LastChatID:  "test-chat-id",
	}

	if state.LastChannel != "test-channel" {
		t.Errorf("Expected LastChannel 'test-channel', got '%s'", state.LastChannel)
	}

	if state.LastChatID != "test-chat-id" {
		t.Errorf("Expected LastChatID 'test-chat-id', got '%s'", state.LastChatID)
	}
}
