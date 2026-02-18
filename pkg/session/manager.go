package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/cache"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/messages"
)

type Session struct {
	Key      string             `json:"key"`
	Messages []messages.Message `json:"messages"`
	Summary  string             `json:"summary,omitempty"`
	Created  time.Time          `json:"created"`
	Updated  time.Time          `json:"updated"`
}

// SessionManagerConfig holds optional configuration for the session manager.
type SessionManagerConfig struct {
	// MaxCachedSessions is the max number of sessions held in the LRU cache.
	// When exceeded, the least-recently-used session is evicted from memory
	// (but remains on disk). Zero means unlimited (all sessions stay in memory).
	MaxCachedSessions int

	// SessionTTL is how long an idle session stays in the in-memory cache.
	// Zero means no expiration (only evicted by LRU pressure).
	SessionTTL time.Duration
}

type SessionManager struct {
	sessions map[string]*Session      // primary store (always authoritative)
	lru      *cache.LRU[string, bool] // tracks access order; value is just a presence flag
	mu       sync.RWMutex
	storage  string
	cfg      SessionManagerConfig
}

func NewSessionManager(storage string) *SessionManager {
	return NewSessionManagerWithConfig(storage, SessionManagerConfig{})
}

// NewSessionManagerWithConfig creates a SessionManager with LRU cache settings.
func NewSessionManagerWithConfig(storage string, cfg SessionManagerConfig) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		storage:  storage,
		cfg:      cfg,
	}

	if cfg.MaxCachedSessions > 0 {
		sm.lru = cache.New(cache.Options[string, bool]{
			MaxSize: cfg.MaxCachedSessions,
			TTL:     cfg.SessionTTL,
			OnEvict: func(key string, _ bool) {
				sm.evictFromMemory(key)
			},
		})
	}

	if storage != "" {
		os.MkdirAll(storage, 0755)
		sm.loadSessions()
	}

	return sm
}

// touchLRU records an access in the LRU tracker, which may evict cold sessions.
func (sm *SessionManager) touchLRU(key string) {
	if sm.lru != nil {
		sm.lru.Set(key, true)
	}
}

// evictFromMemory removes a session from the in-memory map (called by LRU eviction).
// The session persists on disk. It will be lazy-loaded on next access.
func (sm *SessionManager) evictFromMemory(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	// Save to disk before evicting if storage is configured
	if sm.storage != "" {
		sm.saveSessionLocked(key, session)
	}

	delete(sm.sessions, key)
	logger.DebugCF("session", "LRU evicted session from memory",
		map[string]interface{}{"session": key})
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		sm.touchLRU(key)
		return session
	}

	// Try lazy-load from disk if LRU evicted this session
	if sm.storage != "" {
		if loaded := sm.loadSessionFromDisk(key); loaded != nil {
			sm.sessions[key] = loaded
			sm.touchLRU(key)
			return loaded
		}
	}

	session = &Session{
		Key:      key,
		Messages: []messages.Message{},
		Created:  time.Now(),
		Updated:  time.Now(),
	}
	sm.sessions[key] = session
	sm.touchLRU(key)

	return session
}

// loadSessionFromDisk attempts to load a single session from disk.
// Must be called with sm.mu held.
func (sm *SessionManager) loadSessionFromDisk(key string) *Session {
	sessionPath := filepath.Join(sm.storage, key+".json")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil
	}
	return &session
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, messages.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg messages.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		// Try lazy-load from disk before creating a new session
		if sm.storage != "" {
			if loaded := sm.loadSessionFromDisk(sessionKey); loaded != nil {
				session = loaded
				sm.sessions[sessionKey] = session
			}
		}
		if session == nil {
			session = &Session{
				Key:      sessionKey,
				Messages: []messages.Message{},
				Created:  time.Now(),
			}
			sm.sessions[sessionKey] = session
		}
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
	sm.touchLRU(sessionKey)

	// Hard cap: prevent unbounded growth if summarization keeps failing.
	// Keep last 50 messages when we exceed 200.
	const hardCap = 200
	const keepOnOverflow = 50
	if len(session.Messages) > hardCap {
		logger.InfoCF("session", "Session exceeded hard cap, force-truncating",
			map[string]interface{}{
				"session":  sessionKey,
				"messages": len(session.Messages),
				"keep":     keepOnOverflow,
			})
		session.Messages = session.Messages[len(session.Messages)-keepOnOverflow:]
	}
}

func (sm *SessionManager) GetHistory(key string) []messages.Message {
	sm.mu.RLock()
	session, ok := sm.sessions[key]
	sm.mu.RUnlock()

	if !ok {
		// Try lazy-load
		session = sm.ensureLoaded(key)
		if session == nil {
			return []messages.Message{}
		}
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	history := make([]messages.Message, len(session.Messages))
	copy(history, session.Messages)
	return history
}

func (sm *SessionManager) GetSummary(key string) string {
	sm.mu.RLock()
	session, ok := sm.sessions[key]
	sm.mu.RUnlock()

	if !ok {
		session = sm.ensureLoaded(key)
		if session == nil {
			return ""
		}
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return session.Summary
}

// ensureLoaded tries to load a session from disk if it's not in memory.
// Returns the session if found, nil otherwise.
func (sm *SessionManager) ensureLoaded(key string) *Session {
	if sm.storage == "" {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring write lock
	if session, ok := sm.sessions[key]; ok {
		sm.touchLRU(key)
		return session
	}

	loaded := sm.loadSessionFromDisk(key)
	if loaded != nil {
		sm.sessions[key] = loaded
		sm.touchLRU(key)
	}
	return loaded
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		session.Summary = summary
		session.Updated = time.Now()
	}
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	if keepLast <= 0 {
		session.Messages = []messages.Message{}
		session.Updated = time.Now()
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	cutIdx := len(session.Messages) - keepLast

	// Tool-call-aware truncation: don't split tool-call/tool-result pairs.
	// If the first remaining message has role "tool", scan backward to include
	// the preceding "assistant" message that contains the matching tool_calls.
	for cutIdx > 0 && cutIdx < len(session.Messages) && session.Messages[cutIdx].Role == "tool" {
		cutIdx--
	}

	session.Messages = session.Messages[cutIdx:]
	session.Updated = time.Now()
}

// CleanupStale removes sessions that haven't been updated within maxAge.
func (sm *SessionManager) CleanupStale(maxAge time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for key, session := range sm.sessions {
		if session.Updated.Before(cutoff) {
			delete(sm.sessions, key)
			// Also remove the session file if it exists
			if sm.storage != "" {
				sessionPath := filepath.Join(sm.storage, sanitizeFilename(key)+".json")
				os.Remove(sessionPath)
			}
			removed++
		}
	}

	if removed > 0 {
		logger.InfoCF("session", "Cleaned up stale sessions",
			map[string]interface{}{
				"removed": removed,
				"max_age": maxAge.String(),
			})
	}

	return removed
}

// sanitizeFilename converts a session key into a cross-platform safe filename.
// Session keys use "channel:chatID" (e.g. "telegram:123456") but ':' is the
// volume separator on Windows, so filepath.Base would misinterpret the key.
// We replace it with '_'. The original key is preserved inside the JSON file,
// so loadSessions still maps back to the right in-memory key.
func sanitizeFilename(key string) string {
	return strings.ReplaceAll(key, ":", "_")
}
func (sm *SessionManager) Save(key string) error {
	if sm.storage == "" {
		return nil
	}

	filename := sanitizeFilename(key)

	// filepath.IsLocal rejects empty names, "..", absolute paths, and
	// OS-reserved device names (NUL, COM1 … on Windows).
	// The extra checks reject "." and any directory separators so that
	// the session file is always written directly inside sm.storage.
	if filename == "." || !filepath.IsLocal(filename) || strings.ContainsAny(filename, `/\`) {
		return os.ErrInvalid
	}

	// Snapshot under read lock, then perform slow file I/O after unlock.
	sm.mu.RLock()
	stored, ok := sm.sessions[key]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}

	snapshot := snapshotSession(stored)
	sm.mu.RUnlock()

	return sm.writeSessionToDisk(key, &snapshot)
}

// saveSessionLocked saves a session to disk. Caller must hold sm.mu.
func (sm *SessionManager) saveSessionLocked(key string, session *Session) {
	if sm.storage == "" {
		return
	}
	snapshot := snapshotSession(session)
	if err := sm.writeSessionToDisk(key, &snapshot); err != nil {
		logger.WarnCF("session", "Failed to save session to disk before LRU eviction",
			map[string]interface{}{"session": key, "error": err.Error()})
	}
}

func snapshotSession(s *Session) Session {
	snap := Session{
		Key:     s.Key,
		Summary: s.Summary,
		Created: s.Created,
		Updated: s.Updated,
	}
	if len(s.Messages) > 0 {
		snap.Messages = make([]messages.Message, len(s.Messages))
		copy(snap.Messages, s.Messages)
	} else {
		snap.Messages = []messages.Message{}
	}
	return snap
}

func (sm *SessionManager) writeSessionToDisk(key string, session *Session) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}

	sessionPath := filepath.Join(sm.storage, sanitizeFilename(key)+".json")
	tmpFile, err := os.CreateTemp(sm.storage, "session-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// CachedSessionCount returns the number of sessions currently in the in-memory cache.
func (sm *SessionManager) CachedSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		sessionPath := filepath.Join(sm.storage, file.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		sm.sessions[session.Key] = &session
	}

	return nil
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []messages.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		// Create a deep copy to strictly isolate internal state
		// from the caller's slice.
		msgs := make([]messages.Message, len(history))
		copy(msgs, history)
		session.Messages = msgs
		session.Updated = time.Now()
	}
}
