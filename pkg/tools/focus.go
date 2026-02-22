package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/session"
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
)

var focusAgentID = pkg.NAME

// FocusState tracks an active focus investigation.
type FocusState struct {
	Topic           string     `json:"topic"`
	Goal            string     `json:"goal"`
	Steps           []string   `json:"steps"`
	Deadline        string     `json:"deadline"`
	CheckpointIndex int        `json:"checkpoint_index"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Outcome         string     `json:"outcome,omitempty"`
	Status          string     `json:"status,omitempty"`
}

// KnowledgeBlock stores accumulated summaries from completed focus sessions.
type KnowledgeBlock struct {
	Entries []KnowledgeEntry `json:"entries"`
}

// KnowledgeEntry is a single completed focus summary.
type KnowledgeEntry struct {
	Topic       string    `json:"topic"`
	Goal        string    `json:"goal"`
	Steps       []string  `json:"steps,omitempty"`
	Deadline    string    `json:"deadline,omitempty"`
	Outcome     string    `json:"outcome,omitempty"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// FocusText returns the canonical topic/goal label for this focus state.
func (fs FocusState) FocusText() string {
	if strings.TrimSpace(fs.Goal) != "" {
		return fs.Goal
	}
	return fs.Topic
}

// FormatBlock renders active focus metadata for system-prompt injection.
func (fs *FocusState) FormatBlock() string {
	if fs == nil || (strings.TrimSpace(fs.Topic) == "" && strings.TrimSpace(fs.Goal) == "") {
		return ""
	}

	lines := make([]string, 0, 6)
	lines = append(lines, "# Focus")
	lines = append(lines, "")
	lines = append(lines, "## "+fs.FocusText())
	if len(fs.Steps) > 0 {
		lines = append(lines, "### Planned Steps")
		for _, step := range fs.Steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			lines = append(lines, "- "+step)
		}
	}
	if strings.TrimSpace(fs.Deadline) != "" {
		lines = append(lines, "Deadline: "+fs.Deadline)
	}
	if strings.TrimSpace(fs.Status) != "" {
		lines = append(lines, "Status: "+fs.Status)
	}

	return strings.Join(lines, "\n") + "\n"
}

// FormatBlock renders the knowledge block for system prompt injection.
func (kb *KnowledgeBlock) FormatBlock() string {
	if len(kb.Entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Knowledge\n\n")
	for _, e := range kb.Entries {
		title := e.Topic
		if strings.TrimSpace(title) == "" {
			title = e.Goal
		}
		sb.WriteString(fmt.Sprintf("## %s\n", title))
		if strings.TrimSpace(e.Outcome) != "" {
			sb.WriteString(e.Outcome + "\n")
		} else if strings.TrimSpace(e.Summary) != "" {
			sb.WriteString(e.Summary + "\n")
		}
		if len(e.Steps) > 0 {
			sb.WriteString("\n### Steps\n")
			for _, step := range e.Steps {
				step = strings.TrimSpace(step)
				if step == "" {
					continue
				}
				sb.WriteString("- " + step + "\n")
			}
		}
		if strings.TrimSpace(e.Deadline) != "" {
			sb.WriteString("\nDeadline: " + e.Deadline + "\n")
		}
		if !e.CompletedAt.IsZero() {
			sb.WriteString(fmt.Sprintf("Completed at: %s\n", e.CompletedAt.Format(time.RFC3339)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// StartFocusTool declares an investigation topic and creates a checkpoint.
type StartFocusTool struct {
	delegate   KVStore
	sessions   *session.SessionManager
	sessionKey func() string
	OnChange   func() // called after focus state changes; used for cache invalidation
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
			"goal": map[string]interface{}{
				"type":        "string",
				"description": "Optional target outcome for this focus. If set, supersedes topic as the canonical label.",
			},
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Optional ordered list of expected investigation steps.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"deadline": map[string]interface{}{
				"type":        "string",
				"description": "Optional ISO-8601 deadline or scheduling hint (e.g., '2026-02-28T17:00:00Z').",
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
	goal, err := parseOptionalStringArg(args, "goal")
	if err != nil {
		return ErrorResult(fmt.Sprintf("goal %v", err))
	}
	steps, err := parseStringSliceArg(args, "steps")
	if err != nil {
		return ErrorResult(fmt.Sprintf("steps %v", err))
	}
	deadline, err := parseOptionalStringArg(args, "deadline")
	if err != nil {
		return ErrorResult(fmt.Sprintf("deadline %v", err))
	}

	sk := t.sessionKey()
	if sk == "" {
		return ErrorResult("no active session")
	}

	history := t.sessions.GetHistory(sk)
	state := FocusState{
		Topic:           topic,
		Goal:            goal,
		Steps:           steps,
		Deadline:        deadline,
		CheckpointIndex: len(history),
		StartedAt:       time.Now(),
	}

	data, err := jsonv2.Marshal(state)
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
			"goal":       goal,
			"steps":      len(steps),
			"deadline":   deadline,
			"checkpoint": state.CheckpointIndex,
			"session":    sk,
		})

	goalLine := ""
	if strings.TrimSpace(goal) != "" {
		goalLine = fmt.Sprintf("Focus label: %s\n", state.FocusText())
	}

	if t.OnChange != nil {
		t.OnChange()
	}

	return SilentResult(fmt.Sprintf("Focus started on: %s\n%sCheckpoint at message %d. Explore freely, then call complete_focus when done.", topic, goalLine, state.CheckpointIndex))
}

// CompleteFocusTool summarizes findings, persists to knowledge, and prunes context.
type CompleteFocusTool struct {
	delegate   KVStore
	sessions   *session.SessionManager
	sessionKey func() string
	OnChange   func() // called after focus state + knowledge changes; used for cache invalidation
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
			"outcome": map[string]interface{}{
				"type":        "string",
				"description": "Optional explicit outcome statement for this focus.",
			},
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Optional ordered list of steps taken during the focus session.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "Optional completion status (default: completed).",
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
	if err := jsonv2.Unmarshal([]byte(raw), &state); err != nil {
		return ErrorResult(fmt.Sprintf("corrupt focus state: %v", err))
	}

	outcome, err := parseOptionalStringArg(args, "outcome")
	if err != nil {
		return ErrorResult(fmt.Sprintf("outcome %v", err))
	}
	steps, err := parseStringSliceArg(args, "steps")
	if err != nil {
		return ErrorResult(fmt.Sprintf("steps %v", err))
	}
	status, err := parseOptionalStringArg(args, "status")
	if err != nil {
		return ErrorResult(fmt.Sprintf("status %v", err))
	}
	if status == "" {
		status = "completed"
	}
	if len(steps) > 0 {
		state.Steps = steps
	}
	state.Status = status
	completedAt := time.Now()
	state.CompletedAt = &completedAt
	if outcome != "" {
		state.Outcome = outcome
	}

	// Append to knowledge block
	if err := t.appendKnowledge(ctx, sk, state, summary); err != nil {
		return ErrorResult(fmt.Sprintf("save knowledge: %v", err))
	}

	// Prune messages between checkpoint and current position.
	// Keep everything before checkpoint and the last few messages for continuity.
	history := t.sessions.GetHistory(sk)
	pruned := t.pruneHistory(history, state.CheckpointIndex)
	t.sessions.SetHistory(sk, pruned)

	if err := t.delegate.DeleteKV(ctx, focusAgentID, focusKey); err != nil {
		logger.WarnCF("focus", "failed to clean up focus state", map[string]interface{}{"error": err, "session": sk})
	}

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

	if t.OnChange != nil {
		t.OnChange()
	}

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

func (t *CompleteFocusTool) appendKnowledge(ctx context.Context, sessionKey string, state FocusState, summary string) error {
	kvKey := knowledgeKVPrefix + sessionKey

	kb := &KnowledgeBlock{}
	raw, err := t.delegate.GetKV(ctx, focusAgentID, kvKey)
	if err == nil && raw != "" {
		if uerr := jsonv2.Unmarshal([]byte(raw), kb); uerr != nil {
			logger.WarnCF("focus", "corrupt knowledge block, resetting", map[string]interface{}{"error": uerr, "key": kvKey})
		}
	}

	kb.Entries = append(kb.Entries, KnowledgeEntry{
		Topic:       state.Topic,
		Goal:        state.Goal,
		Steps:       state.Steps,
		Deadline:    state.Deadline,
		Outcome:     state.Outcome,
		Summary:     summary,
		CreatedAt:   time.Now(),
		CompletedAt: *state.CompletedAt,
	})

	data, err := jsonv2.Marshal(kb)
	if err != nil {
		return err
	}

	return t.delegate.UpsertKV(ctx, focusAgentID, kvKey, string(data))
}

func parseStringSliceArg(args map[string]interface{}, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok {
		return nil, nil
	}

	var values []string
	switch v := raw.(type) {
	case []string:
		values = append(values, v...)
	case []interface{}:
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", key, i)
			}
			values = append(values, strings.TrimSpace(s))
		}
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}

	normalized := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
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
	if err := jsonv2.Unmarshal([]byte(raw), &kb); err != nil {
		return ""
	}

	return kb.FormatBlock()
}

// LoadFocusState loads an active focus state for the given session.
func LoadFocusState(ctx context.Context, delegate KVStore, sessionKey string) (FocusState, bool) {
	if delegate == nil {
		return FocusState{}, false
	}
	key := focusKVPrefix + sessionKey
	raw, err := delegate.GetKV(ctx, focusAgentID, key)
	if err != nil || raw == "" {
		return FocusState{}, false
	}

	var state FocusState
	if err := jsonv2.Unmarshal([]byte(raw), &state); err != nil {
		logger.WarnCF("focus", "failed to parse active focus state", map[string]interface{}{"error": err, "session": sessionKey})
		return FocusState{}, false
	}
	return state, true
}
