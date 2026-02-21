package tools

import (
	"context"
	"fmt"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"

	fantasy "charm.land/fantasy"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

// LLMMapTool performs schema-validated per-item processing with model calls
// and no side effects (toolless, deterministic structure).
type LLMMapTool struct {
	model   fantasy.LanguageModel
	modelID string
	runtime *MapRuntime
}

func NewLLMMapTool(model fantasy.LanguageModel, modelID string) *LLMMapTool {
	return &LLMMapTool{
		model:   model,
		modelID: modelID,
	}
}

func (t *LLMMapTool) SetRuntime(runtime *MapRuntime) {
	t.runtime = runtime
}

func (t *LLMMapTool) Name() string {
	return "llm_map"
}

func (t *LLMMapTool) Description() string {
	return "Apply an instruction to each item with schema-validated JSON output. Supports worker-backed runs with run handles for large batches."
}

func (t *LLMMapTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"items": map[string]interface{}{
				"type":        "array",
				"description": "Array of items to process.",
			},
			"input_jsonl": map[string]interface{}{
				"type":        "string",
				"description": "Boundary-only JSONL input. Exactly one of items or input_jsonl is required.",
			},
			"instruction": map[string]interface{}{
				"type":        "string",
				"description": "Instruction applied to each item. Output must be JSON object only.",
			},
			"output_schema": map[string]interface{}{
				"type":        "object",
				"description": "Optional lightweight JSON schema (properties + required) used to validate each output object.",
			},
			"execution_mode": map[string]interface{}{
				"type":        "string",
				"description": "inline or worker. Defaults to worker for JSONL/large batches, inline otherwise.",
			},
			"max_retries": map[string]interface{}{
				"type":        "integer",
				"description": "Retries per item after first attempt when using worker execution (default 1, max 10).",
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
		"required": []string{"instruction"},
	}
}

func (t *LLMMapTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.model == nil && t.runtime == nil {
		return ErrorResult("llm_map model is not configured").WithError(fmt.Errorf("llm_map model is nil"))
	}

	items, usedJSONL, err := parseMapBoundaryItems(args)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	instruction, ok := args["instruction"].(string)
	if !ok || strings.TrimSpace(instruction) == "" {
		return ErrorResult("instruction is required").WithError(fmt.Errorf("instruction is required"))
	}

	var schema map[string]interface{}
	if rawSchema, ok := args["output_schema"].(map[string]interface{}); ok {
		schema = rawSchema
	}
	schemaJSON := ""
	if schema != nil {
		b, err := canonicalJSONString(schema)
		if err != nil {
			return ErrorResult("output_schema is not JSON-serializable").WithError(err)
		}
		schemaJSON = b
	}
	maxRetries, err := parseMapMaxRetries(args, 1)
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
			OperatorKind:     MapOperatorLLM,
			Instruction:      strings.TrimSpace(instruction),
			OutputSchemaJSON: schemaJSON,
			MaxRetries:       uint16(maxRetries),
		}, items, idempotencyKey)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to enqueue llm_map run: %v", err)).WithError(err)
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
				return ErrorResult("failed to serialize llm_map enqueue result").WithError(err)
			}
			return &ToolResult{
				ForLLM:  string(data),
				ForUser: string(data),
				Silent:  false,
				IsError: false,
				Async:   false,
			}
		}

		// Inline mode still goes through the worker lifecycle for a single runtime path.
		runFinal, err := t.runtime.ProcessRunToTerminal(ctx, run.ID, len(items)*(maxRetries+2)+32)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to complete inline llm_map run %s: %v", run.ID.String(), err)).WithError(err)
		}
		itemRows, err := t.runtime.ReadRunItems(ctx, run.ID, 0, len(items))
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read inline llm_map results: %v", err)).WithError(err)
		}
		return buildLLMMapInlineResult(runFinal, itemRows)
	}

	// Legacy synchronous path when runtime persistence is not configured.
	if mode == "worker" {
		return ErrorResult("worker mode requires map runtime persistence").WithError(fmt.Errorf("map runtime is nil"))
	}

	results := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		prompt := fmt.Sprintf(
			"Instruction:\n%s\n\nItem JSON:\n%s\n\nReturn exactly one JSON object and nothing else.",
			instruction,
			item.ItemJSON,
		)
		maxOut := int64(512)
		call := fantasy.Call{
			Prompt: fantasy.Prompt{
				fantasy.NewSystemMessage("You are a pure mapper. Return only strict JSON object output."),
				fantasy.NewUserMessage(prompt),
			},
			MaxOutputTokens: &maxOut,
		}
		resp, err := t.model.Generate(ctx, call)
		if err != nil {
			return ErrorResult(fmt.Sprintf("llm_map failed at index %d", item.Index)).WithError(err)
		}

		var out map[string]interface{}
		if err := jsonv2.Unmarshal([]byte(resp.Content.Text()), &out); err != nil {
			return ErrorResult(fmt.Sprintf("llm_map output at index %d is not valid JSON object", item.Index)).WithError(err)
		}
		if schema != nil {
			if err := validateLLMMapOutputSchema(out, schema); err != nil {
				return ErrorResult(fmt.Sprintf("llm_map schema validation failed at index %d: %v", item.Index, err)).WithError(err)
			}
		}
		results = append(results, out)
	}

	payload := map[string]interface{}{
		"count":   len(results),
		"results": results,
	}
	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize llm_map results").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func buildLLMMapInlineResult(run memsqlc.MapRun, items []MapItemProjection) *ToolResult {
	results := make([]map[string]interface{}, 0, len(items))
	failures := make([]map[string]interface{}, 0)
	for _, item := range items {
		if item.Status == mapItemStatusSucceeded {
			outMap, ok := item.Output.(map[string]interface{})
			if !ok {
				outMap = map[string]interface{}{
					"value": item.Output,
				}
			}
			results = append(results, outMap)
			continue
		}
		failures = append(failures, map[string]interface{}{
			"index":    item.Index,
			"status":   item.Status,
			"attempts": item.Attempts,
			"error":    item.Error,
		})
	}
	payload := map[string]interface{}{
		"run_id": run.ID.String(),
		"status": run.Status,
		"count":  len(results),
		"summary": map[string]interface{}{
			"success_count": len(results),
			"failure_count": len(failures),
		},
		"results":  results,
		"failures": failures,
	}
	data, err := jsonv2.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize llm_map inline results").WithError(err)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: string(data),
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func validateLLMMapOutputSchema(out map[string]interface{}, schema map[string]interface{}) error {
	if requiredRaw, ok := schema["required"].([]interface{}); ok {
		for _, r := range requiredRaw {
			key, _ := r.(string)
			if key == "" {
				continue
			}
			if _, exists := out[key]; !exists {
				return fmt.Errorf("missing required field %q", key)
			}
		}
	}

	props, _ := schema["properties"].(map[string]interface{})
	for key, defRaw := range props {
		value, exists := out[key]
		if !exists {
			continue
		}
		def, _ := defRaw.(map[string]interface{})
		wantType, _ := def["type"].(string)
		if wantType == "" {
			continue
		}
		if !matchesJSONType(value, wantType) {
			return fmt.Errorf("field %q expected type %q", key, wantType)
		}
	}
	return nil
}

func matchesJSONType(value interface{}, want string) bool {
	switch want {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}
