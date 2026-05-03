package state

import (
	"context"
	"fmt"
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

	// LastSessionKey is the last non-ephemeral user session processed.
	LastSessionKey string `json:"last_session_key,omitzero"`

	// LastSessionKeysByTarget tracks the last durable session key per channel/chat target.
	LastSessionKeysByTarget map[string]string `json:"last_session_keys_by_target,omitzero"`

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

// Manager manages persistent state through the delegate (agent_kv).
type Manager struct {
	workspace string
	state     *State
	mu        sync.RWMutex
	delegate  memory.MemoryDelegate
}

// NewManager creates a new state manager with the provided delegate.
// The delegate is required for state persistence.
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
	}

	return sm
}

func (sm *Manager) loadFromDelegate() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if v, err := sm.delegate.GetKV(ctx, kvAgentID, "state:json"); err == nil && v != "" {
		_ = jsonv2.Unmarshal([]byte(v), sm.state)
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

// SetLastSessionKey updates the last durable user session key.
func (sm *Manager) SetLastSessionKey(ctx context.Context, sessionKey string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastSessionKey = sessionKey
	sm.state.Timestamp = time.Now()

	return sm.persist(ctx)
}

// SetLastSessionKeyForTarget updates the last durable session key for a specific channel/chat target.
func (sm *Manager) SetLastSessionKeyForTarget(ctx context.Context, channel, chatID, sessionKey string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state.LastSessionKeysByTarget == nil {
		sm.state.LastSessionKeysByTarget = make(map[string]string)
	}
	key := channel + ":" + chatID
	if sessionKey == "" {
		delete(sm.state.LastSessionKeysByTarget, key)
	} else {
		sm.state.LastSessionKeysByTarget[key] = sessionKey
	}
	sm.state.LastSessionKey = sessionKey
	sm.state.Timestamp = time.Now()

	return sm.persist(ctx)
}

// persist writes the current state to the delegate (KV) if available.
// Must be called with the lock held.
func (sm *Manager) persist(ctx context.Context) error {
	if sm.delegate == nil {
		return nil
	}
	return sm.persistToDelegate(ctx)
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

// GetLastSessionKey returns the last durable session key from the state.
func (sm *Manager) GetLastSessionKey() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastSessionKey
}

// GetLastSessionKeyForTarget returns the last durable session key for a specific channel/chat target.
func (sm *Manager) GetLastSessionKeyForTarget(channel, chatID string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.state.LastSessionKeysByTarget == nil {
		return ""
	}
	return sm.state.LastSessionKeysByTarget[channel+":"+chatID]
}

// GetTimestamp returns the timestamp of the last state update.
func (sm *Manager) GetTimestamp() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Timestamp
}
