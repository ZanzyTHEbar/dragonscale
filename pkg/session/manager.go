package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/ZanzyTHEbar/dragonscale/pkg/cache"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
)

type Session struct {
	Key      string             `json:"key"`
	Messages []messages.Message `json:"messages"`
	Summary  string             `json:"summary,omitzero"`
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

// SessionOption configures a SessionManager.
type SessionOption func(*SessionManager)

// WithSessionDelegate injects a memory delegate for DB-backed session persistence.
// When set, sessions persist through recall_items instead of JSON files.
func WithSessionDelegate(del memory.MemoryDelegate, agentID string) SessionOption {
	return func(sm *SessionManager) {
		sm.delegate = del
		sm.agentID = agentID
	}
}

type msgPersistItem struct {
	sessionKey string
	msg        messages.Message
	barrier    chan struct{} // non-nil for flush barriers; worker closes it after draining prior items
}

type SessionManager struct {
	sessions map[string]*Session      // primary store (always authoritative)
	lru      *cache.LRU[string, bool] // tracks access order; value is just a presence flag
	mu       sync.RWMutex
	storage  string
	cfg      SessionManagerConfig
	delegate memory.MemoryDelegate
	agentID  string
	msgChan  chan msgPersistItem // async message persistence; nil when delegate is nil
	msgDone  chan struct{}       // closed when the persist worker exits
}

func NewSessionManager(storage string, opts ...SessionOption) *SessionManager {
	return NewSessionManagerWithConfig(storage, SessionManagerConfig{}, opts...)
}

// NewSessionManagerWithConfig creates a SessionManager with LRU cache settings.
func NewSessionManagerWithConfig(storage string, cfg SessionManagerConfig, opts ...SessionOption) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		storage:  storage,
		cfg:      cfg,
	}

	for _, opt := range opts {
		opt(sm)
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

	if sm.delegate != nil {
		sm.loadSessionsFromDelegate()
		sm.msgChan = make(chan msgPersistItem, 256)
		sm.msgDone = make(chan struct{})
		go sm.msgPersistWorker()
	} else if storage != "" {
		os.MkdirAll(storage, 0755)
		sm.loadSessions()
	}

	return sm
}

func (sm *SessionManager) loadSessionsFromDelegate() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const pageSize = 500
	offset := 0
	sessionKeys := make(map[string]struct{})
	for {
		items, err := sm.delegate.ListRecallItems(ctx, sm.agentID, "", pageSize, offset)
		if err != nil {
			logger.WarnCF("session", "Failed to load sessions from delegate",
				map[string]interface{}{"error": err.Error()})
			return
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if !strings.Contains(item.Tags, "session-message") {
				continue
			}
			sessionKeys[item.SessionKey] = struct{}{}
		}
		if len(items) < pageSize {
			break
		}
		offset += len(items)
	}

	type sessionLister interface {
		ListSessionMessages(ctx context.Context, agentID, sessionKey, role string, limit int) ([]*memory.RecallItem, error)
	}
	lister, hasSessionLister := sm.delegate.(sessionLister)
	sessionsScanned := 0
	pointersUpdated := 0

	for sessionKey := range sessionKeys {
		sessionsScanned++
		session := &Session{
			Key:      sessionKey,
			Messages: []messages.Message{},
			Created:  time.Now(),
			Updated:  time.Time{},
		}
		sm.sessions[sessionKey] = session

		var chronItems []*memory.RecallItem

		if hasSessionLister {
			count, _ := sm.delegate.CountRecallItems(ctx, sm.agentID, sessionKey)
			limit := count + 32
			if limit < 32 {
				limit = 32
			}
			rows, err := lister.ListSessionMessages(ctx, sm.agentID, sessionKey, "", limit)
			if err == nil {
				chronItems = rows
				for _, item := range rows {
					session.Messages = append(session.Messages, messages.Message{
						Role:    item.Role,
						Content: item.Content,
					})
					if item.CreatedAt.Before(session.Created) {
						session.Created = item.CreatedAt
					}
					if item.CreatedAt.After(session.Updated) {
						session.Updated = item.CreatedAt
					}
				}
			}
		}

		if len(session.Messages) == 0 {
			// Fallback when delegate doesn't expose ListSessionMessages.
			offset := 0
			descItems := make([]*memory.RecallItem, 0, pageSize)
			for {
				batch, err := sm.delegate.ListRecallItems(ctx, sm.agentID, sessionKey, pageSize, offset)
				if err != nil {
					logger.WarnCF("session", "Failed fallback session restore page",
						map[string]interface{}{"session": sessionKey, "offset": offset, "error": err.Error()})
					break
				}
				if len(batch) == 0 {
					break
				}
				for _, item := range batch {
					if !strings.Contains(item.Tags, "session-message") {
						continue
					}
					descItems = append(descItems, item)
				}
				if len(batch) < pageSize {
					break
				}
				offset += len(batch)
			}
			for i := len(descItems) - 1; i >= 0; i-- {
				item := descItems[i]
				if !strings.Contains(item.Tags, "session-message") {
					continue
				}
				chronItems = append(chronItems, item)
				session.Messages = append(session.Messages, messages.Message{
					Role:    item.Role,
					Content: item.Content,
				})
				if item.CreatedAt.Before(session.Created) {
					session.Created = item.CreatedAt
				}
				if item.CreatedAt.After(session.Updated) {
					session.Updated = item.CreatedAt
				}
			}
		}

		// Integrity validation and projection pointer persistence for deterministic resume.
		restoredPtr := projectFromItems(chronItems)
		if restoredPtr != nil {
			priorPtr, _ := sm.loadProjectionPointer(ctx, sessionKey)
			sm.validateAndLogRestore(sessionKey, priorPtr, restoredPtr)
			if !projectionPointerEqual(priorPtr, restoredPtr) {
				if err := sm.persistProjectionPointer(ctx, sessionKey, restoredPtr); err != nil {
					logger.WarnCF("session", "Failed to persist projection pointer",
						map[string]interface{}{"session": sessionKey, "error": err.Error()})
				} else {
					pointersUpdated++
				}
			}
		}
	}

	if err := sm.persistProjectionBackfillStatus(ctx, ProjectionBackfillStatus{
		Version:         1,
		SessionsScanned: sessionsScanned,
		PointersUpdated: pointersUpdated,
		CompletedAt:     time.Now().UTC(),
	}); err != nil {
		logger.WarnCF("session", "Failed to persist projection backfill status",
			map[string]interface{}{"error": err.Error()})
	}
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
	if err := jsonv2.Unmarshal(data, &session); err != nil {
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

	if sm.msgChan != nil {
		sm.msgChan <- msgPersistItem{sessionKey: sessionKey, msg: msg}
	}

}

// msgPersistWorker drains msgChan and writes messages to the delegate in the background.
func (sm *SessionManager) msgPersistWorker() {
	defer close(sm.msgDone)
	for item := range sm.msgChan {
		if item.barrier != nil {
			close(item.barrier)
			continue
		}
		sm.persistMessageToDelegate(item.sessionKey, item.msg)
	}
}

func (sm *SessionManager) persistMessageToDelegate(sessionKey string, msg messages.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	now := time.Now()
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    sm.agentID,
		SessionKey: sessionKey,
		Role:       msg.Role,
		Sector:     memory.SectorEpisodic,
		Importance: 0.5,
		Salience:   0.5,
		DecayRate:  0.01,
		Content:    msg.Content,
		Tags:       "session-message",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := sm.delegate.InsertRecallItem(ctx, item); err != nil {
		logger.WarnCF("session", "Failed to persist message to delegate",
			map[string]interface{}{"session": sessionKey, "error": err.Error()})
		return
	}

	// Dual-write: persist verbatim copy to immutable store (LCM ADR-001).
	immutableWriter, ok := sm.delegate.(interface {
		InsertImmutableMessage(ctx context.Context, msg *memory.ImmutableMessage) error
	})
	if ok {
		imMsg := &memory.ImmutableMessage{
			ID:            ids.New(),
			SessionKey:    sessionKey,
			Role:          msg.Role,
			Content:       msg.Content,
			ToolCallID:    msg.ToolCallID,
			ToolCalls:     toolCallsJSON(msg),
			TokenEstimate: estimateTokensSimple(msg.Content),
		}
		if err := immutableWriter.InsertImmutableMessage(ctx, imMsg); err != nil {
			logger.WarnCF("session", "Failed to persist immutable message",
				map[string]interface{}{"session": sessionKey, "error": err.Error()})
		}
	}

	priorPtr, _ := sm.loadProjectionPointer(ctx, sessionKey)
	newPtr := advancePointer(priorPtr, item.ID, now)
	if err := sm.persistProjectionPointer(ctx, sessionKey, newPtr); err != nil {
		logger.WarnCF("session", "Failed to persist projection pointer after append",
			map[string]interface{}{"session": sessionKey, "error": err.Error()})
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

// Flush blocks until all pending async message persists complete.
func (sm *SessionManager) Flush() {
	if sm.msgChan == nil {
		return
	}
	barrier := make(chan struct{})
	sm.msgChan <- msgPersistItem{barrier: barrier}
	<-barrier
}

// Close drains the async message persistence channel and waits for completion.
func (sm *SessionManager) Close() {
	if sm.msgChan != nil {
		close(sm.msgChan)
		<-sm.msgDone
	}
}

func (sm *SessionManager) Save(key string) error {
	if sm.delegate != nil {
		return nil
	}
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
	data, err := jsonv2.Marshal(session, jsontext.WithIndent("  "))
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
		if err := jsonv2.Unmarshal(data, &session); err != nil {
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

func toolCallsJSON(msg messages.Message) string {
	if len(msg.ToolCalls) == 0 {
		return ""
	}
	b, err := jsonv2.Marshal(msg.ToolCalls)
	if err != nil {
		return ""
	}
	return string(b)
}

func estimateTokensSimple(content string) int {
	return (len(content) + 3) / 4
}
