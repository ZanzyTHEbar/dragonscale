package securebus

import (
	"sync"
	"time"
)

// AuditEvent records one tool execution through the SecureBus.
// The log is append-only: entries are never modified or deleted.
type AuditEvent struct {
	RequestID    string    `json:"request_id"`
	SessionKey   string    `json:"session_key,omitempty"`
	ToolCallID   string    `json:"tool_call_id,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	CommandType  string    `json:"command_type"`
	Depth        uint8     `json:"depth,omitempty"`
	At           time.Time `json:"at"`
	DurationMS   int64     `json:"duration_ms"`
	IsError      bool      `json:"is_error,omitempty"`
	LeakDetected bool      `json:"leak_detected,omitempty"`
	RedactedKeys []string  `json:"redacted_keys,omitempty"`

	// Capability grants observed at execution time.
	SecretsAccessed []string `json:"secrets_accessed,omitempty"`
	PolicyViolation string   `json:"policy_violation,omitempty"`
}

// AuditLog is a thread-safe, in-memory append-only audit log.
// For production deployments, attach a Sink to persist events to the DB.
type AuditLog struct {
	mu     sync.RWMutex
	events []AuditEvent
	sinks  []AuditSink
}

// AuditSink is an optional interface for persisting audit events externally
// (e.g., to the agent_audit_log SQLite table).
type AuditSink interface {
	Write(event AuditEvent) error
}

// NewAuditLog creates an empty in-memory audit log. Pass sinks to fan-out
// writes to external storage.
func NewAuditLog(sinks ...AuditSink) *AuditLog {
	return &AuditLog{sinks: sinks}
}

// Append records an audit event. Returns the first sink error encountered,
// but always appends to the in-memory log.
func (al *AuditLog) Append(event AuditEvent) error {
	al.mu.Lock()
	al.events = append(al.events, event)
	al.mu.Unlock()

	var firstErr error
	for _, sink := range al.sinks {
		if err := sink.Write(event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Events returns a snapshot of all recorded events.
func (al *AuditLog) Events() []AuditEvent {
	al.mu.RLock()
	defer al.mu.RUnlock()
	cp := make([]AuditEvent, len(al.events))
	copy(cp, al.events)
	return cp
}

// Len returns the number of recorded events.
func (al *AuditLog) Len() int {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return len(al.events)
}

// FilterBySession returns all events for a given session key.
func (al *AuditLog) FilterBySession(sessionKey string) []AuditEvent {
	al.mu.RLock()
	defer al.mu.RUnlock()
	var result []AuditEvent
	for _, e := range al.events {
		if e.SessionKey == sessionKey {
			result = append(result, e)
		}
	}
	return result
}

// LeakEvents returns all events where LeakDetected is true.
func (al *AuditLog) LeakEvents() []AuditEvent {
	al.mu.RLock()
	defer al.mu.RUnlock()
	var result []AuditEvent
	for _, e := range al.events {
		if e.LeakDetected {
			result = append(result, e)
		}
	}
	return result
}
