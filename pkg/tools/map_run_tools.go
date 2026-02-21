package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	jsonv2 "github.com/go-json-experiment/json"
)

type MapRunStatusTool struct {
	runtime *MapRuntime
}

func NewMapRunStatusTool(runtime *MapRuntime) *MapRunStatusTool {
	return &MapRunStatusTool{runtime: runtime}
}

func (t *MapRunStatusTool) Name() string {
	return "map_run_status"
}

func (t *MapRunStatusTool) Description() string {
	return "Return current status/progress for an llm_map or agentic_map run."
}

func (t *MapRunStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"run_id": map[string]interface{}{
				"type":        "string",
				"description": "Map run ID returned by llm_map or agentic_map.",
			},
			"process_steps": map[string]interface{}{
				"type":        "integer",
				"description": "Optional number of worker steps to execute before reading status (default 8).",
			},
		},
		"required": []string{"run_id"},
	}
}

func (t *MapRunStatusTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.runtime == nil {
		return ErrorResult("map runtime is not configured").WithError(fmt.Errorf("map runtime is nil"))
	}
	runIDRaw, ok := args["run_id"].(string)
	if !ok || strings.TrimSpace(runIDRaw) == "" {
		return ErrorResult("run_id is required").WithError(fmt.Errorf("run_id is required"))
	}
	runID, err := ids.Parse(strings.TrimSpace(runIDRaw))
	if err != nil {
		return ErrorResult("run_id must be a valid UUID").WithError(err)
	}
	steps, err := parseMapLimit(args, "process_steps", 8, 500)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	if steps > 0 {
		if _, err := t.runtime.ProcessPending(ctx, steps); err != nil {
			return ErrorResult(fmt.Sprintf("failed to process map jobs: %v", err)).WithError(err)
		}
	}
	run, err := t.runtime.GetRun(ctx, runID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to load map run: %v", err)).WithError(err)
	}
	payload := toMapRunProjection(run)
	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize map run status").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

type MapRunReadTool struct {
	runtime *MapRuntime
}

func NewMapRunReadTool(runtime *MapRuntime) *MapRunReadTool {
	return &MapRunReadTool{runtime: runtime}
}

func (t *MapRunReadTool) Name() string {
	return "map_run_read"
}

func (t *MapRunReadTool) Description() string {
	return "Read paged item results for a map run; output as JSON or generated JSONL."
}

func (t *MapRunReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"run_id": map[string]interface{}{
				"type":        "string",
				"description": "Map run ID returned by llm_map or agentic_map.",
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Pagination offset (default 0).",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Pagination limit (default 100, max 500).",
			},
			"format": map[string]interface{}{
				"type":        "string",
				"description": "Projection format: json (default) or jsonl.",
			},
			"process_steps": map[string]interface{}{
				"type":        "integer",
				"description": "Optional worker steps to process before reading (default 8).",
			},
		},
		"required": []string{"run_id"},
	}
}

func (t *MapRunReadTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.runtime == nil {
		return ErrorResult("map runtime is not configured").WithError(fmt.Errorf("map runtime is nil"))
	}
	runIDRaw, ok := args["run_id"].(string)
	if !ok || strings.TrimSpace(runIDRaw) == "" {
		return ErrorResult("run_id is required").WithError(fmt.Errorf("run_id is required"))
	}
	runID, err := ids.Parse(strings.TrimSpace(runIDRaw))
	if err != nil {
		return ErrorResult("run_id must be a valid UUID").WithError(err)
	}

	offset, err := parseMapLimit(args, "offset", 0, 1_000_000)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	limit, err := parseMapLimit(args, "limit", defaultMapReadLimit, maxMapReadLimit)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	format, err := parseMapReadFormat(args)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	steps, err := parseMapLimit(args, "process_steps", 8, 500)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	if steps > 0 {
		if _, err := t.runtime.ProcessPending(ctx, steps); err != nil {
			return ErrorResult(fmt.Sprintf("failed to process map jobs: %v", err)).WithError(err)
		}
	}

	run, err := t.runtime.GetRun(ctx, runID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to load map run: %v", err)).WithError(err)
	}
	items, err := t.runtime.ReadRunItems(ctx, runID, offset, limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to load map run items: %v", err)).WithError(err)
	}

	payload := map[string]interface{}{
		"run":    toMapRunProjection(run),
		"offset": offset,
		"limit":  limit,
		"format": format,
	}
	if format == "jsonl" {
		jsonlText, err := encodeMapItemsJSONL(items)
		if err != nil {
			return ErrorResult("failed to encode JSONL projection").WithError(err)
		}
		payload["items_jsonl"] = jsonlText
		payload["item_count"] = len(items)
	} else {
		payload["items"] = items
	}

	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize map run page").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func encodeMapItemsJSONL(items []MapItemProjection) (string, error) {
	if len(items) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		b, err := jsonv2.Marshal(item)
		if err != nil {
			return "", err
		}
		lines = append(lines, string(b))
	}
	return strings.Join(lines, "\n"), nil
}
