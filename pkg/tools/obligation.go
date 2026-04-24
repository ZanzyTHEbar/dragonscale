package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

const obligationKVPrefix = "obligation:"

type ObligationState string

const (
	ObligationStateCreated   ObligationState = "created"
	ObligationStateScheduled ObligationState = "scheduled"
	ObligationStateDue       ObligationState = "due"
	ObligationStateExecuted  ObligationState = "executed"
	ObligationStateVerified  ObligationState = "verified"
)

type ObligationEvidence struct {
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
	Content string    `json:"content"`
}

type ObligationRecord struct {
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Details     string               `json:"details,omitempty"`
	State       ObligationState      `json:"state"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	ScheduledAt time.Time            `json:"scheduled_at,omitempty"`
	DueAt       time.Time            `json:"due_at,omitempty"`
	ExecutedAt  time.Time            `json:"executed_at,omitempty"`
	VerifiedAt  time.Time            `json:"verified_at,omitempty"`
	Evidence    []ObligationEvidence `json:"evidence,omitempty"`
}

type ObligationTool struct {
	delegate memory.MemoryDelegate
	agentID  string
}

func NewObligationTool(delegate memory.MemoryDelegate, agentID string) *ObligationTool {
	return &ObligationTool{
		delegate: delegate,
		agentID:  agentID,
	}
}

func (t *ObligationTool) Name() string {
	return "obligation"
}

func (t *ObligationTool) Description() string {
	return "Manage commitment/reminder obligations with lifecycle states and audit evidence."
}

func (t *ObligationTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "create|get|list|update_state|add_evidence",
			},
			"obligation_id": map[string]interface{}{
				"type":        "string",
				"description": "Obligation ID for get/update_state/add_evidence.",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Title for create action. Alias: content.",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Alias for title when creating an obligation.",
			},
			"details": map[string]interface{}{
				"type":        "string",
				"description": "Optional details for create action. Aliases: notes, description.",
			},
			"notes": map[string]interface{}{
				"type":        "string",
				"description": "Alias for details when creating an obligation.",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Alias for details when creating an obligation.",
			},
			"scheduled_at": map[string]interface{}{
				"type":        "string",
				"description": "Optional schedule time. Accepts RFC3339 and naive YYYY-MM-DDTHH:MM:SS. Aliases: remind_at, reminder_at.",
			},
			"remind_at": map[string]interface{}{
				"type":        "string",
				"description": "Alias for scheduled_at when creating an obligation.",
			},
			"reminder_at": map[string]interface{}{
				"type":        "string",
				"description": "Alias for scheduled_at when creating an obligation.",
			},
			"due_at": map[string]interface{}{
				"type":        "string",
				"description": "Optional due time. Accepts RFC3339 and naive YYYY-MM-DDTHH:MM:SS. Aliases: due_date, deadline_at.",
			},
			"due_date": map[string]interface{}{
				"type":        "string",
				"description": "Alias for due_at when creating an obligation.",
			},
			"deadline_at": map[string]interface{}{
				"type":        "string",
				"description": "Alias for due_at when creating an obligation.",
			},
			"state": map[string]interface{}{
				"type":        "string",
				"description": "Target lifecycle state for update_state.",
			},
			"evidence": map[string]interface{}{
				"type":        "string",
				"description": "Evidence text for add_evidence.",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Optional evidence source label.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ObligationTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.delegate == nil {
		return ErrorResult("obligation store is not configured").WithError(fmt.Errorf("obligation delegate is nil"))
	}
	action, _ := args["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "create":
		return t.create(ctx, args)
	case "get":
		return t.get(ctx, args)
	case "list":
		return t.list(ctx)
	case "update_state":
		return t.updateState(ctx, args)
	case "add_evidence":
		return t.addEvidence(ctx, args)
	default:
		return ErrorResult("unknown obligation action").WithError(fmt.Errorf("unknown obligation action: %s", action))
	}
}

func (t *ObligationTool) create(ctx context.Context, args map[string]interface{}) *ToolResult {
	title := obligationFirstNonEmptyString(args, "title", "content")
	if strings.TrimSpace(title) == "" {
		return ErrorResult("title is required for create").WithError(fmt.Errorf("title is required"))
	}
	now := time.Now().UTC()
	rec := &ObligationRecord{
		ID:        ids.New().String(),
		Title:     title,
		Details:   obligationFirstNonEmptyString(args, "details", "notes", "description"),
		State:     ObligationStateCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if scheduledAtRaw := obligationFirstNonEmptyString(args, "scheduled_at", "remind_at", "reminder_at"); scheduledAtRaw != "" {
		ts, err := parseObligationTimestamp(scheduledAtRaw)
		if err != nil {
			return ErrorResult("scheduled_at must be RFC3339 or YYYY-MM-DDTHH:MM:SS").WithError(err)
		}
		rec.ScheduledAt = ts.UTC()
		rec.State = ObligationStateScheduled
	}
	if dueAtRaw := obligationFirstNonEmptyString(args, "due_at", "due_date", "deadline_at"); dueAtRaw != "" {
		ts, err := parseObligationTimestamp(dueAtRaw)
		if err != nil {
			return ErrorResult("due_at must be RFC3339 or YYYY-MM-DDTHH:MM:SS").WithError(err)
		}
		rec.DueAt = ts.UTC()
		if rec.State == ObligationStateCreated {
			rec.State = ObligationStateScheduled
		}
	}

	if err := t.saveRecord(ctx, rec); err != nil {
		return ErrorResult("failed to persist obligation").WithError(err)
	}
	return obligationSuccess(rec)
}

func (t *ObligationTool) get(ctx context.Context, args map[string]interface{}) *ToolResult {
	id := stringOr(args["obligation_id"])
	if id == "" {
		return ErrorResult("obligation_id is required").WithError(fmt.Errorf("obligation_id is required"))
	}
	rec, err := t.loadRecord(ctx, id)
	if err != nil {
		return ErrorResult("failed to load obligation").WithError(err)
	}
	return obligationSuccess(rec)
}

func (t *ObligationTool) list(ctx context.Context) *ToolResult {
	rows, err := t.delegate.ListKVByPrefix(ctx, t.agentID, obligationKVPrefix, 500)
	if err != nil {
		return ErrorResult("failed to list obligations").WithError(err)
	}
	result := make([]*ObligationRecord, 0, len(rows))
	for _, raw := range rows {
		var rec ObligationRecord
		if err := jsonv2.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		result = append(result, &rec)
	}
	data, err := jsonv2.Marshal(map[string]interface{}{
		"count":       len(result),
		"obligations": result,
	})
	if err != nil {
		return ErrorResult("failed to serialize obligations").WithError(err)
	}
	return &ToolResult{ForLLM: string(data), ForUser: string(data), Silent: false, IsError: false}
}

// CollectDueObligations returns obligations that are due at or before now.
// Scheduled obligations that become due are atomically transitioned to "due"
// with a scheduler evidence record.
func (t *ObligationTool) CollectDueObligations(ctx context.Context, now time.Time, source string) ([]*ObligationRecord, error) {
	if t.delegate == nil {
		return nil, fmt.Errorf("obligation delegate is nil")
	}

	now = now.UTC()
	source = strings.TrimSpace(source)

	rows, err := t.delegate.ListKVByPrefix(ctx, t.agentID, obligationKVPrefix, 500)
	if err != nil {
		return nil, fmt.Errorf("list obligations: %w", err)
	}

	due := make([]*ObligationRecord, 0, len(rows))
	for _, raw := range rows {
		var rec ObligationRecord
		if err := jsonv2.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		if !obligationIsDue(&rec, now) {
			continue
		}

		if rec.State == ObligationStateScheduled {
			rec.State = ObligationStateDue
			rec.UpdatedAt = now
			if rec.DueAt.IsZero() {
				rec.DueAt = now
			}
			if source != "" {
				rec.Evidence = append(rec.Evidence, ObligationEvidence{
					At:      now,
					Source:  source,
					Content: "obligation became due via scheduler check",
				})
			}
			if err := t.saveRecord(ctx, &rec); err != nil {
				return nil, fmt.Errorf("persist due obligation %s: %w", rec.ID, err)
			}
		}

		recCopy := rec
		due = append(due, &recCopy)
	}

	sort.SliceStable(due, func(i, j int) bool {
		left := obligationSortTime(due[i])
		right := obligationSortTime(due[j])
		if left.Equal(right) {
			return due[i].ID < due[j].ID
		}
		return left.Before(right)
	})

	return due, nil
}

func (t *ObligationTool) updateState(ctx context.Context, args map[string]interface{}) *ToolResult {
	id := stringOr(args["obligation_id"])
	if id == "" {
		return ErrorResult("obligation_id is required").WithError(fmt.Errorf("obligation_id is required"))
	}
	next := ObligationState(stringOr(args["state"]))
	if !isValidObligationState(next) {
		return ErrorResult("invalid obligation state").WithError(fmt.Errorf("invalid obligation state: %s", next))
	}
	rec, err := t.loadRecord(ctx, id)
	if err != nil {
		return ErrorResult("failed to load obligation").WithError(err)
	}
	if err := validateObligationTransition(rec.State, next, len(rec.Evidence)); err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	now := time.Now().UTC()
	rec.State = next
	rec.UpdatedAt = now
	switch next {
	case ObligationStateDue:
		if rec.DueAt.IsZero() {
			rec.DueAt = now
		}
	case ObligationStateExecuted:
		rec.ExecutedAt = now
	case ObligationStateVerified:
		rec.VerifiedAt = now
	}
	if err := t.saveRecord(ctx, rec); err != nil {
		return ErrorResult("failed to persist obligation state").WithError(err)
	}
	return obligationSuccess(rec)
}

func (t *ObligationTool) addEvidence(ctx context.Context, args map[string]interface{}) *ToolResult {
	id := stringOr(args["obligation_id"])
	if id == "" {
		return ErrorResult("obligation_id is required").WithError(fmt.Errorf("obligation_id is required"))
	}
	evidence := stringOr(args["evidence"])
	if strings.TrimSpace(evidence) == "" {
		return ErrorResult("evidence is required").WithError(fmt.Errorf("evidence is required"))
	}
	rec, err := t.loadRecord(ctx, id)
	if err != nil {
		return ErrorResult("failed to load obligation").WithError(err)
	}
	rec.Evidence = append(rec.Evidence, ObligationEvidence{
		At:      time.Now().UTC(),
		Source:  stringOr(args["source"]),
		Content: evidence,
	})
	rec.UpdatedAt = time.Now().UTC()
	if err := t.saveRecord(ctx, rec); err != nil {
		return ErrorResult("failed to persist obligation evidence").WithError(err)
	}
	return obligationSuccess(rec)
}

func (t *ObligationTool) loadRecord(ctx context.Context, id string) (*ObligationRecord, error) {
	raw, err := t.delegate.GetKV(ctx, t.agentID, obligationKVPrefix+id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("obligation %s not found", id)
	}
	var rec ObligationRecord
	if err := jsonv2.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (t *ObligationTool) saveRecord(ctx context.Context, rec *ObligationRecord) error {
	data, err := jsonv2.Marshal(rec)
	if err != nil {
		return err
	}
	return t.delegate.UpsertKV(ctx, t.agentID, obligationKVPrefix+rec.ID, string(data))
}

func validateObligationTransition(current, next ObligationState, evidenceCount int) error {
	if current == next {
		return nil
	}
	allowed := map[ObligationState]map[ObligationState]bool{
		ObligationStateCreated: {
			ObligationStateScheduled: true,
			ObligationStateDue:       true,
		},
		ObligationStateScheduled: {
			ObligationStateDue:      true,
			ObligationStateExecuted: true,
		},
		ObligationStateDue: {
			ObligationStateExecuted: true,
		},
		ObligationStateExecuted: {
			ObligationStateVerified: true,
		},
		ObligationStateVerified: {},
	}
	if !allowed[current][next] {
		return fmt.Errorf("invalid obligation transition: %s -> %s", current, next)
	}
	if next == ObligationStateVerified && evidenceCount == 0 {
		return fmt.Errorf("verified state requires evidence")
	}
	return nil
}

func obligationIsDue(rec *ObligationRecord, now time.Time) bool {
	switch rec.State {
	case ObligationStateScheduled:
		if !rec.DueAt.IsZero() {
			return !rec.DueAt.After(now)
		}
		if !rec.ScheduledAt.IsZero() {
			return !rec.ScheduledAt.After(now)
		}
		return false
	case ObligationStateDue:
		return true
	default:
		return false
	}
}

func obligationSortTime(rec *ObligationRecord) time.Time {
	if rec == nil {
		return time.Time{}
	}
	if !rec.DueAt.IsZero() {
		return rec.DueAt
	}
	if !rec.ScheduledAt.IsZero() {
		return rec.ScheduledAt
	}
	if !rec.UpdatedAt.IsZero() {
		return rec.UpdatedAt
	}
	return rec.CreatedAt
}

func isValidObligationState(s ObligationState) bool {
	switch s {
	case ObligationStateCreated, ObligationStateScheduled, ObligationStateDue, ObligationStateExecuted, ObligationStateVerified:
		return true
	default:
		return false
	}
}

func obligationSuccess(rec *ObligationRecord) *ToolResult {
	data, _ := jsonv2.Marshal(rec)
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func stringOr(v interface{}) string {
	s, _ := v.(string)
	return s
}

func obligationFirstNonEmptyString(args map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringOr(args[key])); value != "" {
			return value
		}
	}
	return ""
}

func parseObligationTimestamp(raw string) (time.Time, error) {
	ts, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return ts, nil
	}
	return time.Parse("2006-01-02T15:04:05", raw)
}
