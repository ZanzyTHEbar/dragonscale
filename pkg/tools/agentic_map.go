package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	jsonv2 "github.com/go-json-experiment/json"
)

// AgenticMapTool maps tasks over items using subagent execution with retries.
type AgenticMapTool struct {
	manager       *SubagentManager
	originChannel string
	originChatID  string
	runtime       *MapRuntime
}

func NewAgenticMapTool(manager *SubagentManager) *AgenticMapTool {
	return &AgenticMapTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *AgenticMapTool) SetRuntime(runtime *MapRuntime) {
	t.runtime = runtime
}

func (t *AgenticMapTool) Name() string {
	return "agentic_map"
}

func (t *AgenticMapTool) Description() string {
	return "Run subagent processing over each item with retries. Supports worker-backed runs and generated run reads."
}

func (t *AgenticMapTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"items": map[string]interface{}{
				"type":        "array",
				"description": "Items to process via subagent execution.",
			},
			"input_jsonl": map[string]interface{}{
				"type":        "string",
				"description": "Boundary-only JSONL input. Exactly one of items or input_jsonl is required.",
			},
			"task_template": map[string]interface{}{
				"type":        "string",
				"description": "Task template. Supports {{item_json}} and {{index}} placeholders.",
			},
			"max_retries": map[string]interface{}{
				"type":        "integer",
				"description": "Retries per item after the first attempt (default 1).",
			},
			"delegated_scope": map[string]interface{}{
				"type":        "string",
				"description": "Delegated scope metadata passed to nested subagent calls.",
			},
			"kept_work": map[string]interface{}{
				"type":        "string",
				"description": "Kept work metadata passed to nested subagent calls.",
			},
			"execution_mode": map[string]interface{}{
				"type":        "string",
				"description": "inline or worker. Defaults to worker for JSONL/large batches, inline otherwise.",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional map run session key for persistence (default: default).",
			},
			"idempotency_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional key to deduplicate repeated run creation.",
			},
		},
		"required": []string{"task_template"},
	}
}

func (t *AgenticMapTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *AgenticMapTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.manager == nil && t.runtime == nil {
		return ErrorResult("agentic_map manager is not configured").WithError(fmt.Errorf("agentic_map manager is nil"))
	}
	originChannel, originChatID := ResolveExecutionTarget(ctx, t.originChannel, t.originChatID)

	items, usedJSONL, err := parseMapBoundaryItems(args)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	taskTemplate, ok := args["task_template"].(string)
	if !ok || strings.TrimSpace(taskTemplate) == "" {
		return ErrorResult("task_template is required").WithError(fmt.Errorf("task_template is required"))
	}
	if !strings.Contains(taskTemplate, "{{item_json}}") || !strings.Contains(taskTemplate, "{{index}}") {
		return ErrorResult("task_template must include both {{item_json}} and {{index}} placeholders").
			WithError(fmt.Errorf("task_template missing required placeholders"))
	}

	maxRetries, err := parseMapMaxRetries(args, 1)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	delegatedScope, err := parseOptionalStringArg(args, "delegated_scope")
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	keptWork, err := parseOptionalStringArg(args, "kept_work")
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	mode, err := resolveMapExecutionMode(args, len(items), usedJSONL)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	sessionKey, err := parseOptionalStringArg(args, "session_key")
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	idempotencyKey, err := parseOptionalStringArg(args, "idempotency_key")
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	if sessionKey == "" {
		sessionKey = "default"
	}

	if t.runtime != nil {
		run, reused, err := t.runtime.EnqueueRun(ctx, sessionKey, MapRunSpec{
			OperatorKind:   MapOperatorAgentic,
			TaskTemplate:   taskTemplate,
			MaxRetries:     uint16(maxRetries),
			DelegatedScope: delegatedScope,
			KeptWork:       keptWork,
			OriginChannel:  originChannel,
			OriginChatID:   originChatID,
		}, items, idempotencyKey)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to enqueue agentic_map run: %v", err)).WithError(err)
		}
		if mode == "worker" {
			payload := map[string]interface{}{
				"run_id":           run.ID.String(),
				"status":           run.Status,
				"accepted_count":   len(items),
				"queued_count":     run.QueuedItems,
				"execution_mode":   "worker",
				"idempotent_reuse": reused,
			}
			data, err := jsonv2.Marshal(payload)
			if err != nil {
				return ErrorResult("failed to serialize agentic_map enqueue result").WithError(err)
			}
			return &ToolResult{
				ForLLM:  string(data),
				ForUser: string(data),
				Silent:  false,
				IsError: false,
				Async:   false,
			}
		}
		runFinal, err := t.runtime.ProcessRunToTerminal(ctx, run.ID, len(items)*(maxRetries+2)+32)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to complete inline agentic_map run %s: %v", run.ID.String(), err)).WithError(err)
		}
		itemRows, err := t.runtime.ReadRunItems(ctx, run.ID, 0, len(items))
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read inline agentic_map results: %v", err)).WithError(err)
		}
		return buildAgenticMapInlineResult(runFinal, itemRows, maxRetries)
	}

	// Legacy synchronous path when runtime persistence is not configured.
	if mode == "worker" {
		return ErrorResult("worker mode requires map runtime persistence").WithError(fmt.Errorf("map runtime is nil"))
	}
	if t.manager == nil {
		return ErrorResult("agentic_map manager is not configured").WithError(fmt.Errorf("agentic_map manager is nil"))
	}

	subTool := NewSubagentTool(t.manager)
	subTool.SetContext(originChannel, originChatID)

	type itemResult struct {
		Index    int    `json:"index"`
		Success  bool   `json:"success"`
		Attempts int    `json:"attempts"`
		Output   string `json:"output,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	results := make([]itemResult, 0, len(items))
	successCount := 0

	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return ErrorResult("agentic_map cancelled before completion").WithError(err)
		}

		task := strings.ReplaceAll(taskTemplate, "{{item_json}}", item.ItemJSON)
		task = strings.ReplaceAll(task, "{{index}}", strconv.Itoa(i))

		attempts := 0
		var lastErr string
		var output string
		okItem := false
		for attempts <= maxRetries {
			if err := ctx.Err(); err != nil {
				lastErr = err.Error()
				break
			}
			attempts++
			callArgs := map[string]interface{}{
				"task":  task,
				"label": fmt.Sprintf("agentic-map-%d", i),
			}
			if delegatedScope != "" {
				callArgs["delegated_scope"] = delegatedScope
			}
			if keptWork != "" {
				callArgs["kept_work"] = keptWork
			}

			r := subTool.Execute(ctx, callArgs)
			if r != nil && !r.IsError {
				okItem = true
				output = r.ForUser
				break
			}
			if r != nil {
				lastErr = r.ForLLM
			} else {
				lastErr = "unknown subagent error"
			}
		}

		if okItem {
			successCount++
		}
		results = append(results, itemResult{
			Index:    i,
			Success:  okItem,
			Attempts: attempts,
			Output:   output,
			Error:    lastErr,
		})
	}

	payload := map[string]interface{}{
		"count": len(results),
		"summary": map[string]interface{}{
			"success_count": successCount,
			"failure_count": len(results) - successCount,
			"max_retries":   maxRetries,
		},
		"results": results,
	}
	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize agentic_map results").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func buildAgenticMapInlineResult(run memsqlc.MapRun, items []MapItemProjection, maxRetries int) *ToolResult {
	type itemResult struct {
		Index    int64  `json:"index"`
		Success  bool   `json:"success"`
		Attempts int64  `json:"attempts"`
		Output   string `json:"output,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	results := make([]itemResult, 0, len(items))
	successCount := 0
	for _, item := range items {
		outText := ""
		switch out := item.Output.(type) {
		case map[string]interface{}:
			if forUser, ok := out["for_user"].(string); ok {
				outText = forUser
			}
		case string:
			outText = out
		}
		ok := item.Status == mapItemStatusSucceeded
		if ok {
			successCount++
		}
		results = append(results, itemResult{
			Index:    item.Index,
			Success:  ok,
			Attempts: item.Attempts,
			Output:   outText,
			Error:    item.Error,
		})
	}
	payload := map[string]interface{}{
		"run_id": run.ID.String(),
		"status": run.Status,
		"count":  len(results),
		"summary": map[string]interface{}{
			"success_count": successCount,
			"failure_count": len(results) - successCount,
			"max_retries":   maxRetries,
		},
		"results": results,
	}
	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize agentic_map inline results").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}
