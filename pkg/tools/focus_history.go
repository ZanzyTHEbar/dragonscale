package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
)

const defaultFocusHistoryLimit = 5
const maxFocusHistoryLimit = 25

// FocusHistoryTool lets the agent query completed focus entries.
type FocusHistoryTool struct {
	delegate   KVStore
	sessionKey func() string
}

func NewFocusHistoryTool(delegate KVStore, sessionKeyFn func() string) *FocusHistoryTool {
	return &FocusHistoryTool{
		delegate:   delegate,
		sessionKey: sessionKeyFn,
	}
}

func (t *FocusHistoryTool) Name() string { return "focus_history" }

func (t *FocusHistoryTool) Description() string {
	return "Search and retrieve completed focus session history entries for reflection, handoff, and continuity."
}

func (t *FocusHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Optional substring to search across goal/topic/steps/outcome/summary.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of history items to return. Defaults to 5, max 25.",
			},
		},
	}
}

func (t *FocusHistoryTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	sessionKey := ""
	if t.sessionKey != nil {
		sessionKey = t.sessionKey()
	}
	if strings.TrimSpace(sessionKey) == "" {
		return ErrorResult("no active session")
	}

	encountered, err := t.loadCompletedFocus(ctx, sessionKey)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if len(encountered) == 0 {
		return &ToolResult{ForLLM: "No completed focus history found."}
	}

	query, _ := args["query"].(string)
	query = strings.ToLower(strings.TrimSpace(query))

	limit := defaultFocusHistoryLimit
	if raw, ok := args["limit"]; ok {
		switch l := raw.(type) {
		case int:
			limit = l
		case int64:
			limit = int(l)
		case float64:
			limit = int(l)
		case string:
			if strings.TrimSpace(l) == "" {
				break
			}
			if parsed, parseErr := parseIntArg(l); parseErr != nil {
				return ErrorResult("invalid limit")
			} else {
				limit = parsed
			}
		default:
			return ErrorResult("invalid limit")
		}
	}
	if limit <= 0 {
		limit = defaultFocusHistoryLimit
	}
	if limit > maxFocusHistoryLimit {
		limit = maxFocusHistoryLimit
	}

	filtered := filterFocusHistory(encountered, query)
	if len(filtered) == 0 {
		return &ToolResult{ForLLM: "No focus history matches the query."}
	}

	if limit > len(filtered) {
		limit = len(filtered)
	}
	filtered = filtered[:limit]

	response := struct {
		Query string           `json:"query"`
		Count int              `json:"count"`
		Total int              `json:"total"`
		Items []KnowledgeEntry `json:"items"`
	}{query, len(filtered), len(filtered), filtered}

	payload, err := jsonv2.Marshal(response)
	if err != nil {
		return ErrorResult("failed to marshal focus history")
	}
	return &ToolResult{ForLLM: string(payload)}
}

func parseIntArg(value string) (int, error) {
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}

func (t *FocusHistoryTool) loadCompletedFocus(ctx context.Context, sessionKey string) ([]KnowledgeEntry, error) {
	knowledge, err := t.delegate.GetKV(ctx, focusAgentID, knowledgeKVPrefix+sessionKey)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(knowledge) == "" {
		return nil, nil
	}

	var block KnowledgeBlock
	if err := jsonv2.Unmarshal([]byte(knowledge), &block); err != nil {
		return nil, err
	}

	entries := append([]KnowledgeEntry(nil), block.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CompletedAt.After(entries[j].CompletedAt)
	})

	return entries, nil
}

func filterFocusHistory(entries []KnowledgeEntry, query string) []KnowledgeEntry {
	if query == "" {
		return entries
	}

	filtered := make([]KnowledgeEntry, 0, len(entries))
	for _, entry := range entries {
		if matchesFocusHistoryEntry(entry, query) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func matchesFocusHistoryEntry(entry KnowledgeEntry, query string) bool {
	fields := []string{
		entry.Topic,
		entry.Goal,
		entry.Outcome,
		entry.Summary,
		strings.Join(entry.Steps, " "),
		entry.Deadline,
	}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}
