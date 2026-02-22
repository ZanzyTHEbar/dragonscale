package state

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

var kvAgentID = pkg.NAME

// State represents the persistent state for a workspace.
// It includes information about the last active channel/chat.
type State struct {
	// LastChannel is the last channel used for communication
	LastChannel string `json:"last_channel,omitzero"`

	// LastChatID is the last chat ID used for communication
	LastChatID string `json:"last_chat_id,omitzero"`

	// Timestamp is the last time this state was updated
	Timestamp time.Time `json:"timestamp"`
}

// Option configures a Manager.
type Option func(*Manager)

// WithDelegate injects a memory delegate for KV-backed persistence.
// When set, state is stored in the agent_kv table instead of on disk.
func WithDelegate(del memory.MemoryDelegate) Option {
	return func(m *Manager) { m.delegate = del }
}

// Manager manages persistent state with atomic saves.
// When a delegate is present, state persists through agent_kv.
// Otherwise, it falls back to file-based atomic JSON writes.
type Manager struct {
	workspace string
	state     *State
	mu        sync.RWMutex
	stateFile string
	delegate  memory.MemoryDelegate
}

// NewManager creates a new state manager for the given workspace.
func NewManager(workspace string, opts ...Option) *Manager {
	sm := &Manager{
		workspace: workspace,
		state:     &State{},
	}

	for _, opt := range opts {
		opt(sm)
	}

	if sm.delegate != nil {
		sm.loadFromDelegate()
		return sm
	}

	stateDir := filepath.Join(workspace, "state")
	stateFile := filepath.Join(stateDir, "state.json")
	oldStateFile := filepath.Join(workspace, "state.json")

	os.MkdirAll(stateDir, 0755)
	sm.stateFile = stateFile

	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		if data, err := os.ReadFile(oldStateFile); err == nil {
			if err := jsonv2.Unmarshal(data, sm.state); err == nil {
				sm.saveAtomic()
				log.Printf("[INFO] state: migrated state from %s to %s", oldStateFile, stateFile)
			}
		}
	} else {
		sm.load()
	}

	return sm
}

func (sm *Manager) loadFromDelegate() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Prefer single-key format (state:json)
	if v, err := sm.delegate.GetKV(ctx, kvAgentID, "state:json"); err == nil && v != "" {
		if err := jsonv2.Unmarshal([]byte(v), sm.state); err == nil {
			return
		}
	}

	// Fallback: legacy 3-key format
	if v, err := sm.delegate.GetKV(ctx, kvAgentID, "state:last_channel"); err == nil && v != "" {
		sm.state.LastChannel = v
	}
	if v, err := sm.delegate.GetKV(ctx, kvAgentID, "state:last_chat_id"); err == nil && v != "" {
		sm.state.LastChatID = v
	}
	if v, err := sm.delegate.GetKV(ctx, kvAgentID, "state:timestamp"); err == nil && v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			sm.state.Timestamp = t
		}
	}
}

// SetLastChannel atomically updates the last channel and saves the state.
func (sm *Manager) SetLastChannel(ctx context.Context, channel string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastChannel = channel
	sm.state.Timestamp = time.Now()

	return sm.persist(ctx)
}

// SetLastChatID atomically updates the last chat ID and saves the state.
func (sm *Manager) SetLastChatID(ctx context.Context, chatID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastChatID = chatID
	sm.state.Timestamp = time.Now()

	return sm.persist(ctx)
}

// SetChannelAndChatID atomically updates both channel and chat ID in a single persist.
func (sm *Manager) SetChannelAndChatID(ctx context.Context, channel, chatID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastChannel = channel
	sm.state.LastChatID = chatID
	sm.state.Timestamp = time.Now()

	return sm.persist(ctx)
}

// persist writes the current state to the delegate (KV) or file.
// Must be called with the lock held.
func (sm *Manager) persist(ctx context.Context) error {
	if sm.delegate != nil {
		return sm.persistToDelegate(ctx)
	}
	return sm.saveAtomic()
}

func (sm *Manager) persistToDelegate(ctx context.Context) error {
	blob, err := jsonv2.Marshal(sm.state, jsontext.WithIndent(""))
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := sm.delegate.UpsertKV(ctx, kvAgentID, "state:json", string(blob)); err != nil {
		return fmt.Errorf("upsert state: %w", err)
	}
	return nil
}

// GetLastChannel returns the last channel from the state.
func (sm *Manager) GetLastChannel() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChannel
}

// GetLastChatID returns the last chat ID from the state.
func (sm *Manager) GetLastChatID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChatID
}

// GetTimestamp returns the timestamp of the last state update.
func (sm *Manager) GetTimestamp() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Timestamp
}

// saveAtomic performs an atomic save using temp file + rename.
// This ensures that the state file is never corrupted:
// 1. Write to a temp file
// 2. Rename temp file to target (atomic on POSIX systems)
// 3. If rename fails, cleanup the temp file
//
// Must be called with the lock held.
func (sm *Manager) saveAtomic() error {
	// Create temp file in the same directory as the target
	tempFile := sm.stateFile + ".tmp"

	// Marshal state to JSON
	data, err := jsonv2.Marshal(sm.state, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temp file
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename from temp to target
	if err := os.Rename(tempFile, sm.stateFile); err != nil {
		// Cleanup temp file if rename fails
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// load loads the state from disk.
func (sm *Manager) load() error {
	data, err := os.ReadFile(sm.stateFile)
	if err != nil {
		// File doesn't exist yet, that's OK
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	if err := jsonv2.Unmarshal(data, sm.state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return nil
}
