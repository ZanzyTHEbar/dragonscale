package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/messages"
	"github.com/sipeed/picoclaw/pkg/session"
)

// KVStore is the minimal interface needed for focus persistence.
type KVStore interface {
	GetKV(ctx context.Context, agentID, key string) (string, error)
	UpsertKV(ctx context.Context, agentID, key, value string) error
	DeleteKV(ctx context.Context, agentID, key string) error
}

const (
	focusKVPrefix     = "focus:"
	knowledgeKVPrefix = "knowledge:"
	focusAgentID      = "picoclaw"
)

// FocusState tracks an active focus investigation.
type FocusState struct {
	Topic           string    `json:"topic"`
	CheckpointIndex int       `json:"checkpoint_index"`
	StartedAt       time.Time `json:"started_at"`
}

// KnowledgeBlock stores accumulated summaries from completed focus sessions.
type KnowledgeBlock struct {
	Entries []KnowledgeEntry `json:"entries"`
}

// KnowledgeEntry is a single completed focus summary.
type KnowledgeEntry struct {
	Topic     string    `json:"topic"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// FormatBlock renders the knowledge block for system prompt injection.
func (kb *KnowledgeBlock) FormatBlock() string {
	if len(kb.Entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Knowledge\n\n")
	for _, e := range kb.Entries {
		sb.WriteString(fmt.Sprintf("## %s\n%s\n\n", e.Topic, e.Summary))
	}
	return sb.String()
}

// StartFocusTool declares an investigation topic and creates a checkpoint.
type StartFocusTool struct {
	delegate   KVStore
	sessions   *session.SessionManager
	sessionKey func() string
}

func NewStartFocusTool(delegate KVStore, sessions *session.SessionManager, sessionKeyFn func() string) *StartFocusTool {
	return &StartFocusTool{
		delegate:   delegate,
		sessions:   sessions,
		sessionKey: sessionKeyFn,
	}
}

func (t *StartFocusTool) Name() string { return "start_focus" }

func (t *StartFocusTool) Description() string {
	return "Declare an investigation topic and create a context checkpoint. After exploring the topic (typically 10-15 tool calls), use complete_focus to summarize findings and prune working context. This keeps your context window lean and focused."
}

func (t *StartFocusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"topic": map[string]interface{}{
				"type":        "string",
				"description": "What you are investigating or working on (e.g., 'debug authentication flow', 'implement caching layer')",
			},
		},
		"required": []string{"topic"},
	}
}

func (t *StartFocusTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	topic, _ := args["topic"].(string)
	if topic == "" {
		return ErrorResult("topic is required")
	}

	sk := t.sessionKey()
	if sk == "" {
		return ErrorResult("no active session")
	}

	history := t.sessions.GetHistory(sk)
	state := FocusState{
		Topic:           topic,
		CheckpointIndex: len(history),
		StartedAt:       time.Now(),
	}

	data, err := json.Marshal(state)
	if err != nil {
		return ErrorResult(fmt.Sprintf("marshal focus state: %v", err))
	}

	key := focusKVPrefix + sk
	if err := t.delegate.UpsertKV(ctx, focusAgentID, key, string(data)); err != nil {
		return ErrorResult(fmt.Sprintf("save focus state: %v", err))
	}

	logger.InfoCF("focus", "Focus started",
		map[string]interface{}{
			"topic":      topic,
			"checkpoint": state.CheckpointIndex,
			"session":    sk,
		})

	return SilentResult(fmt.Sprintf("Focus started on: %s\nCheckpoint at message %d. Explore freely, then call complete_focus when done.", topic, state.CheckpointIndex))
}

// CompleteFocusTool summarizes findings, persists to knowledge, and prunes context.
type CompleteFocusTool struct {
	delegate   KVStore
	sessions   *session.SessionManager
	sessionKey func() string
}

func NewCompleteFocusTool(delegate KVStore, sessions *session.SessionManager, sessionKeyFn func() string) *CompleteFocusTool {
	return &CompleteFocusTool{
		delegate:   delegate,
		sessions:   sessions,
		sessionKey: sessionKeyFn,
	}
}

func (t *CompleteFocusTool) Name() string { return "complete_focus" }

func (t *CompleteFocusTool) Description() string {
	return "Complete a focus investigation. Provide a summary of what was attempted, what was learned, and the outcome. The summary is saved to persistent Knowledge and the investigation messages are pruned from context, freeing tokens for future work."
}

func (t *CompleteFocusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"summary": map[string]interface{}{
				"type":        "string",
				"description": "Concise summary of the investigation: what was attempted, what was learned, and the outcome",
			},
		},
		"required": []string{"summary"},
	}
}

func (t *CompleteFocusTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	summary, _ := args["summary"].(string)
	if summary == "" {
		return ErrorResult("summary is required")
	}

	sk := t.sessionKey()
	if sk == "" {
		return ErrorResult("no active session")
	}

	focusKey := focusKVPrefix + sk
	raw, err := t.delegate.GetKV(ctx, focusAgentID, focusKey)
	if err != nil || raw == "" {
		return ErrorResult("no active focus session — call start_focus first")
	}

	var state FocusState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ErrorResult(fmt.Sprintf("corrupt focus state: %v", err))
	}

	// Append to knowledge block
	if err := t.appendKnowledge(ctx, sk, state.Topic, summary); err != nil {
		return ErrorResult(fmt.Sprintf("save knowledge: %v", err))
	}

	// Prune messages between checkpoint and current position.
	// Keep everything before checkpoint and the last few messages for continuity.
	history := t.sessions.GetHistory(sk)
	pruned := t.pruneHistory(history, state.CheckpointIndex)
	t.sessions.SetHistory(sk, pruned)

	// Clean up focus state
	_ = t.delegate.DeleteKV(ctx, focusAgentID, focusKey)

	msgsBefore := len(history)
	msgsAfter := len(pruned)
	logger.InfoCF("focus", "Focus completed",
		map[string]interface{}{
			"topic":       state.Topic,
			"msgs_before": msgsBefore,
			"msgs_after":  msgsAfter,
			"pruned":      msgsBefore - msgsAfter,
			"session":     sk,
		})

	return SilentResult(fmt.Sprintf(
		"Focus completed: '%s'\nSummary saved to Knowledge block.\nPruned %d messages (%d → %d). Context is now lean.",
		state.Topic, msgsBefore-msgsAfter, msgsBefore, msgsAfter,
	))
}

// pruneHistory removes investigation messages between checkpoint and end,
// keeping pre-checkpoint context and a small tail for continuity.
func (t *CompleteFocusTool) pruneHistory(history []messages.Message, checkpointIdx int) []messages.Message {
	if checkpointIdx >= len(history) {
		return history
	}

	const keepTail = 4

	pre := history[:checkpointIdx]

	tailStart := len(history) - keepTail
	if tailStart < checkpointIdx {
		tailStart = checkpointIdx
	}

	// Tool-call-aware: don't start tail on a "tool" message
	for tailStart > checkpointIdx && tailStart < len(history) && history[tailStart].Role == "tool" {
		tailStart--
	}

	tail := history[tailStart:]

	result := make([]messages.Message, 0, len(pre)+len(tail))
	result = append(result, pre...)
	result = append(result, tail...)
	return result
}

func (t *CompleteFocusTool) appendKnowledge(ctx context.Context, sessionKey, topic, summary string) error {
	kvKey := knowledgeKVPrefix + sessionKey

	kb := &KnowledgeBlock{}
	raw, err := t.delegate.GetKV(ctx, focusAgentID, kvKey)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), kb)
	}

	kb.Entries = append(kb.Entries, KnowledgeEntry{
		Topic:     topic,
		Summary:   summary,
		CreatedAt: time.Now(),
	})

	data, err := json.Marshal(kb)
	if err != nil {
		return err
	}

	return t.delegate.UpsertKV(ctx, focusAgentID, kvKey, string(data))
}

// LoadKnowledgeBlock loads the persistent knowledge block for a session.
func LoadKnowledgeBlock(ctx context.Context, delegate KVStore, sessionKey string) string {
	if delegate == nil {
		return ""
	}
	kvKey := knowledgeKVPrefix + sessionKey
	raw, err := delegate.GetKV(ctx, focusAgentID, kvKey)
	if err != nil || raw == "" {
		return ""
	}

	var kb KnowledgeBlock
	if err := json.Unmarshal([]byte(raw), &kb); err != nil {
		return ""
	}

	return kb.FormatBlock()
}
