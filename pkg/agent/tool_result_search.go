package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

type ToolResultSearchView struct {
	StartLine  int `json:"start_line,omitempty" description:"Optional. 1-indexed start line (inclusive)."`
	EndLine    int `json:"end_line,omitempty" description:"Optional. 1-indexed end line (inclusive)."`
	MaxLines   int `json:"max_lines,omitempty" description:"Optional. Default 30, max 200."`
	StartChunk int `json:"start_chunk,omitempty" description:"Optional. 0-indexed start chunk (inclusive)."`
	EndChunk   int `json:"end_chunk,omitempty" description:"Optional. 0-indexed end chunk (inclusive)."`
	MaxChunks  int `json:"max_chunks,omitempty" description:"Optional. Default 3, max 20."`
}

type ToolResultSearchInput struct {
	ConversationID string `json:"conversation_id,omitempty" description:"Optional. Agent conversation UUID."`
	RunID          string `json:"run_id,omitempty" description:"Optional. Agent run UUID."`
	ToolCallID     string `json:"tool_call_id,omitempty" description:"Optional. Tool call id to fetch (requires run_id)."`
	ToolName       string `json:"tool_name,omitempty" description:"Optional. Filter by tool name."`
	Query          string `json:"query,omitempty" description:"Optional. Case-insensitive substring match on tool_name/tool_call_id/summary."`

	Limit int                   `json:"limit,omitempty" description:"Optional. Default 5, max 50."`
	View  *ToolResultSearchView `json:"view,omitempty" description:"Optional. File view range for each result."`
}

// NewToolResultSearchTool creates the tool_result_search agent tool for
// querying previously offloaded tool results.
func NewToolResultSearchTool(q *sqlc.Queries, kv KVDelegate) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"tool_result_search",
		"Search previously stored tool results for this agent. Supports viewing a line range or chunk range from stored results.",
		func(ctx context.Context, input ToolResultSearchInput, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			_ = call

			if q == nil {
				return fantasy.NewTextErrorResponse("db is not configured"), nil
			}
			if kv == nil {
				return fantasy.NewTextErrorResponse("KV delegate is not configured"), nil
			}

			limit := input.Limit
			if limit <= 0 {
				limit = 5
			}
			if limit > 50 {
				limit = 50
			}

			rows, err := loadToolResultRows(ctx, q, input)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			query := strings.ToLower(strings.TrimSpace(input.Query))
			toolName := strings.TrimSpace(input.ToolName)

			filtered := make([]sqlc.AgentToolResult, 0, len(rows))
			for _, r := range rows {
				if toolName != "" && r.ToolName != toolName {
					continue
				}
				if query != "" {
					if !strings.Contains(strings.ToLower(r.ToolName), query) &&
						!strings.Contains(strings.ToLower(r.ToolCallID), query) &&
						(r.Preview == nil || !strings.Contains(strings.ToLower(*r.Preview), query)) {
						continue
					}
				}
				filtered = append(filtered, r)
				if len(filtered) >= limit {
					break
				}
			}

			view := input.View
			startLine, endLine := normalizeLineView(view)
			startChunk, endChunk := normalizeChunkView(view)

			type item struct {
				ID         string          `json:"id"`
				RunID      string          `json:"run_id"`
				StepIndex  int64           `json:"step_index"`
				ToolCallID string          `json:"tool_call_id"`
				ToolName   string          `json:"tool_name"`
				Preview    *string         `json:"preview,omitempty"`
				FullKey    string          `json:"full_key"`
				ChunkCount int64           `json:"chunk_count"`
				View       string          `json:"view"`
				ViewRange  map[string]int  `json:"view_range"`
				Metadata   json.RawMessage `json:"metadata_json"`
			}

			out := struct {
				Total int    `json:"total"`
				Items []item `json:"items"`
			}{
				Total: len(filtered),
				Items: make([]item, 0, len(filtered)),
			}

			for _, r := range filtered {
				sel, viewRange, loadErr := loadView(ctx, kv, r, startLine, endLine, startChunk, endChunk)
				if loadErr != nil {
					sel = "ERROR: " + loadErr.Error()
					viewRange = map[string]int{
						"start_line":  startLine,
						"end_line":    endLine,
						"start_chunk": startChunk,
						"end_chunk":   endChunk,
					}
				}

				out.Items = append(out.Items, item{
					ID:         r.ID.String(),
					RunID:      r.RunID.String(),
					StepIndex:  r.StepIndex,
					ToolCallID: r.ToolCallID,
					ToolName:   r.ToolName,
					Preview:    r.Preview,
					FullKey:    r.FullKey,
					ChunkCount: r.ChunkCount,
					View:       sel,
					ViewRange:  viewRange,
					Metadata:   r.MetadataJson,
				})
			}

			b, _ := json.Marshal(out)
			return fantasy.NewTextResponse(string(b)), nil
		},
	)
}

func loadToolResultRows(ctx context.Context, q *sqlc.Queries, input ToolResultSearchInput) ([]sqlc.AgentToolResult, error) {
	if q == nil {
		return nil, pcerrors.New(pcerrors.CodeUnknown, "db is not configured")
	}

	if strings.TrimSpace(input.RunID) != "" && strings.TrimSpace(input.ToolCallID) != "" {
		runID, err := ids.Parse(strings.TrimSpace(input.RunID))
		if err != nil {
			return nil, err
		}
		row, err := q.GetAgentToolResultByRunIDAndToolCallID(ctx, sqlc.GetAgentToolResultByRunIDAndToolCallIDParams{
			RunID:      runID,
			ToolCallID: strings.TrimSpace(input.ToolCallID),
		})
		if err != nil {
			return nil, err
		}
		return []sqlc.AgentToolResult{row}, nil
	}

	if strings.TrimSpace(input.RunID) != "" {
		runID, err := ids.Parse(strings.TrimSpace(input.RunID))
		if err != nil {
			return nil, err
		}
		return q.ListAgentToolResultsByRunID(ctx, sqlc.ListAgentToolResultsByRunIDParams{
			RunID: runID,
		})
	}

	if strings.TrimSpace(input.ConversationID) != "" {
		conversationID, err := ids.Parse(strings.TrimSpace(input.ConversationID))
		if err != nil {
			return nil, err
		}
		return q.ListAgentToolResultsByConversationID(ctx, sqlc.ListAgentToolResultsByConversationIDParams{
			ConversationID: conversationID,
		})
	}

	return nil, pcerrors.New(pcerrors.CodeUnknown, "conversation_id or run_id is required (and tool_call_id requires run_id)")
}

func normalizeLineView(v *ToolResultSearchView) (startLine int, endLine int) {
	startLine = 1
	maxLines := 30

	if v == nil {
		return 1, 30
	}
	if v.StartLine > 0 {
		startLine = v.StartLine
	}
	if v.MaxLines > 0 {
		maxLines = v.MaxLines
	}
	if maxLines > 200 {
		maxLines = 200
	}
	if v.EndLine > 0 {
		endLine = v.EndLine
	} else {
		endLine = startLine + maxLines - 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	return startLine, endLine
}

func normalizeChunkView(v *ToolResultSearchView) (startChunk int, endChunk int) {
	startChunk = 0
	maxChunks := 3

	if v == nil {
		return 0, 2
	}
	if v.StartChunk > 0 {
		startChunk = v.StartChunk
	}
	if v.MaxChunks > 0 {
		maxChunks = v.MaxChunks
	}
	if maxChunks > 20 {
		maxChunks = 20
	}
	if v.EndChunk > 0 {
		endChunk = v.EndChunk
	} else {
		endChunk = startChunk + maxChunks - 1
	}
	if endChunk < startChunk {
		endChunk = startChunk
	}
	return startChunk, endChunk
}

func loadView(ctx context.Context, kv KVDelegate, row sqlc.AgentToolResult, startLine, endLine, startChunk, endChunk int) (string, map[string]int, error) {
	if kv == nil {
		return "", nil, pcerrors.New(pcerrors.CodeUnknown, "KV delegate is nil")
	}

	if row.ChunkCount > 0 {
		if startChunk < 0 {
			startChunk = 0
		}
		if int64(endChunk) >= row.ChunkCount {
			endChunk = int(row.ChunkCount - 1)
		}
		if endChunk < startChunk {
			endChunk = startChunk
		}

		baseDir := strings.TrimSuffix(row.FullKey, "/full.json")
		var b strings.Builder
		for i := startChunk; i <= endChunk; i++ {
			chunkKey := fmt.Sprintf("%s/chunks/%06d.txt", baseDir, i)
			part, err := kv.Get(ctx, chunkKey)
			if err != nil {
				return "", nil, err
			}
			b.Write(part)
		}
		return b.String(), map[string]int{"start_chunk": startChunk, "end_chunk": endChunk}, nil
	}

	raw, err := kv.Get(ctx, row.FullKey)
	if err != nil {
		return "", nil, err
	}
	lines := strings.Split(string(raw), "\n")

	sl := startLine
	el := endLine
	if sl < 1 {
		sl = 1
	}
	if sl > len(lines) {
		sl = len(lines)
	}
	if el > len(lines) {
		el = len(lines)
	}
	if el < sl {
		el = sl
	}

	return strings.Join(lines[sl-1:el], "\n"), map[string]int{"start_line": sl, "end_line": el}, nil
}
