package memory

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

type legacyState struct {
	LastChannel string    `json:"last_channel,omitempty"`
	LastChatID  string    `json:"last_chat_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// MigrateState performs a one-time migration of workspace/state/state.json
// into agent_kv rows. Uses a marker file to ensure idempotency.
func MigrateState(ctx context.Context, workspace string, delegate MemoryDelegate, agentID string) error {
	if delegate == nil {
		return nil
	}

	stateDir := filepath.Join(workspace, "state")
	markerFile := filepath.Join(stateDir, ".state_kv_migrated")

	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	stateFile := filepath.Join(stateDir, "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var s legacyState
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("[WARN] migrate_state: failed to parse %s: %v", stateFile, err)
		return nil
	}

	if s.LastChannel != "" {
		if err := delegate.UpsertKV(ctx, agentID, "state:last_channel", s.LastChannel); err != nil {
			return err
		}
	}
	if s.LastChatID != "" {
		if err := delegate.UpsertKV(ctx, agentID, "state:last_chat_id", s.LastChatID); err != nil {
			return err
		}
	}
	if !s.Timestamp.IsZero() {
		if err := delegate.UpsertKV(ctx, agentID, "state:timestamp", s.Timestamp.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	os.MkdirAll(stateDir, 0755)
	marker := []byte(time.Now().Format(time.RFC3339))
	if err := os.WriteFile(markerFile, marker, 0644); err != nil {
		log.Printf("[WARN] migrate_state: failed to write marker: %v", err)
	}

	log.Printf("[INFO] migrate_state: migrated state.json to agent_kv")
	return nil
}
