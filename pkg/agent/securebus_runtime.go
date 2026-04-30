package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// SecureBusToolRuntime is a fantasy.ToolRuntime that routes every tool call
// through the SecureBus, then persists the final post-policy result through the
// offloading runtime.
//
// Pipeline per tool call:
//  1. Serialize tool call args → ToolRequest
//  2. bus.Execute() → capability check, secret injection, output scan, audit
//  3. If bus returns a policy error, short-circuit with that error result
//  4. Persist the final result through the offloading runtime
type SecureBusToolRuntime struct {
	// Offloader persists tool results and applies large-result truncation.
	Offloader OffloadingToolRuntime

	// FantasyTools allows execution of agent-only fantasy tools that are not part
	// of the raw registry/securebus path.
	FantasyTools map[string]fantasy.AgentTool

	// Bus is required.
	Bus *securebus.Bus

	// SessionKey is forwarded to bus requests for audit tracing.
	SessionKey string

	// Channel and ChatID restore the registry execution context that contextual
	// and async tools expect on the main runtime path.
	Channel string
	ChatID  string

	// UserPrompt allows lightweight repair of placeholder tool arguments when
	// the user provided an explicit literal value in the request.
	UserPrompt string

	// Optional state persistence for runtime execution.
	StateStore *StateStore
	RunID      ids.UUID
}

// Execute implements fantasy.ToolRuntime.
func (r SecureBusToolRuntime) Execute(
	ctx context.Context,
	_ []fantasy.AgentTool,
	execProviderTools []fantasy.ExecutableProviderTool,
	toolCalls []fantasy.ToolCallContent,
	onResult func(fantasy.ToolResultContent) error,
) ([]fantasy.ToolResultContent, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	if r.Bus == nil {
		return nil, fmt.Errorf("secure bus runtime requires bus")
	}
	if r.Offloader.KV == nil {
		return nil, fmt.Errorf("secure bus runtime requires offloader")
	}

	results := make([]fantasy.ToolResultContent, 0, len(toolCalls))
	stepIndex := fantasy.StepIndexFromCtx(ctx)
	execProviderToolMap := make(map[string]fantasy.ExecutableProviderTool, len(execProviderTools))
	for _, tool := range execProviderTools {
		execProviderToolMap[tool.GetName()] = tool
	}

	var finalState *runtimeStepState
	defer func() {
		if finalState != nil {
			r.recordRunState(context.WithoutCancel(ctx), finalState.step, finalState.state, finalState.snapshot)
		}
	}()

	for i, tc := range toolCalls {
		tc = repairToolCallInput(tc, r.UserPrompt)
		step := stepIndex
		execCtx := tools.WithExecutionTarget(ctx, r.Channel, r.ChatID)
		finalState = &runtimeStepState{step: step, state: "tool_call", snapshot: map[string]any{
			"tool_name":       tc.ToolName,
			"tool_call_index": i,
		}}

		if ft, ok := r.FantasyTools[tc.ToolName]; ok {
			tr, err := executeFantasyTool(ctx, ft, tc)
			if err != nil {
				return results, err
			}
			persisted, err := r.Offloader.PersistResults(fantasy.WithStepIndex(ctx, step), step, []fantasy.ToolCallContent{tc}, []fantasy.ToolResultContent{tr})
			if err != nil {
				return results, err
			}
			for _, pr := range persisted {
				results = append(results, pr)
				finalState = &runtimeStepState{step: step, state: "tool_result", snapshot: map[string]any{
					"tool_name":       tc.ToolName,
					"tool_call_index": i,
				}}
				if onResult != nil {
					if err := onResult(pr); err != nil {
						return results, err
					}
				}
			}
			continue
		}
		if _, ok := execProviderToolMap[tc.ToolName]; ok {
			return results, fmt.Errorf("secure bus runtime does not execute provider-defined tool %q", tc.ToolName)
		}

		reqID := ids.New().String()
		req := itr.NewToolExecRequest(reqID, r.SessionKey, tc.ToolCallID, tc.ToolName, tc.Input)
		busResp := r.Bus.Execute(execCtx, req)

		if busResp.IsError {
			sanitized := sanitizePolicyError(busResp.Result)
			result := fantasy.ToolResultContent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Result:     fantasy.ToolResultOutputContentError{Error: errors.New(sanitized)},
			}
			persisted, err := r.Offloader.PersistResults(fantasy.WithStepIndex(ctx, step), step, []fantasy.ToolCallContent{tc}, []fantasy.ToolResultContent{result})
			if err != nil {
				return results, err
			}
			tr := persisted[0]
			finalState = &runtimeStepState{step: step, state: "tool_call_error", snapshot: map[string]any{
				"tool_name":  tc.ToolName,
				"error":      busResp.Result,
				"error_safe": sanitized,
			}}
			results = append(results, tr)
			if onResult != nil {
				if err := onResult(tr); err != nil {
					return results, err
				}
			}
			continue
		}

		result := fantasy.ToolResultContent{
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.ToolName,
			Result:     fantasy.ToolResultOutputContentText{Text: busResp.Result},
		}
		persisted, err := r.Offloader.PersistResults(fantasy.WithStepIndex(ctx, step), step, []fantasy.ToolCallContent{tc}, []fantasy.ToolResultContent{result})
		if err != nil {
			return results, err
		}

		for _, br := range persisted {
			results = append(results, br)
			finalState = &runtimeStepState{step: step, state: "tool_result", snapshot: map[string]any{
				"tool_name":       tc.ToolName,
				"tool_call_index": i,
			}}
			if onResult != nil {
				if err := onResult(br); err != nil {
					return results, err
				}
			}
		}
	}

	return results, nil
}

type runtimeStepState struct {
	step     int
	state    string
	snapshot map[string]any
}

func (r SecureBusToolRuntime) recordRunState(ctx context.Context, stepIndex int, state string, snapshot map[string]any) {
	if r.StateStore == nil || r.RunID.IsZero() {
		return
	}
	if _, err := r.StateStore.AddRunState(ctx, r.RunID, stepIndex, fantasy.ReActState(state), snapshot); err != nil {
		logger.WarnCF("agent", "Failed to record runtime step state", map[string]any{
			"run_id":     r.RunID.String(),
			"step_index": stepIndex,
			"state":      state,
			"error":      err.Error(),
		})
	}
}

// sanitizePolicyError strips internal details from policy/bus errors before
// they reach the LLM. The full error is preserved in audit state only.
func sanitizePolicyError(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "command timed out"),
		strings.Contains(lower, "timeout"),
		strings.Contains(lower, "exceeds timeout budget"),
		strings.Contains(lower, "command is required"),
		strings.Contains(lower, "command cannot be empty"),
		strings.Contains(lower, "no-op placeholder"),
		strings.Contains(lower, "shell execution is disabled"),
		strings.Contains(lower, "working_dir blocked"),
		strings.Contains(lower, "path is required"),
		strings.Contains(lower, "file not found"),
		strings.Contains(lower, "skill") && strings.Contains(lower, "not found"),
		strings.Contains(lower, "exit code"):
		return raw
	case strings.Contains(lower, "recursion depth"):
		return "policy violation: recursion limit exceeded"
	case strings.Contains(lower, "network access denied"):
		return "policy violation: network access denied"
	case strings.Contains(lower, "filesystem access denied"):
		return "policy violation: filesystem access denied"
	case strings.Contains(lower, "secret injection failed"):
		return "policy violation: unable to resolve required secrets"
	case strings.Contains(lower, "invalid args json"):
		return "policy violation: invalid tool arguments"
	case strings.Contains(lower, "policy violation"):
		return "policy violation: access denied"
	default:
		return "tool execution denied"
	}
}

func repairToolCallInput(tc fantasy.ToolCallContent, userPrompt string) fantasy.ToolCallContent {
	if strings.TrimSpace(userPrompt) == "" || strings.TrimSpace(tc.Input) == "" {
		return tc
	}

	switch tc.ToolName {
	case "exec":
		if command := explicitExecCommand(userPrompt); command != "" {
			if repaired, ok := forceDirectArg(tc.Input, "command", command); ok {
				tc.Input = repaired
			}
		}
	case "skill_read":
		if skillName := explicitSkillName(userPrompt); skillName != "" {
			if repaired, ok := repairDirectArg(tc.Input, "name", skillName); ok {
				tc.Input = repaired
			}
		}
	case "read_file":
		if path := explicitReadFilePath(userPrompt); path != "" {
			if repaired, ok := repairDirectArg(tc.Input, "path", path); ok {
				tc.Input = repaired
			}
		}
	case "write_file":
		if path, content := explicitWriteFileRequest(userPrompt); path != "" || content != "" {
			if repaired, ok := repairWriteFileInput(tc.Input, path, content); ok {
				tc.Input = repaired
			}
		}
	case "list_dir":
		if path := explicitListDirPath(userPrompt); path != "" {
			if repaired, ok := repairDirectArg(tc.Input, "path", path); ok {
				tc.Input = repaired
			}
		}
	case "web_fetch":
		if url := explicitFetchURL(userPrompt); url != "" {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Input), &args); err == nil && isMalformedURLValue(stringArg(args["url"])) {
				args["url"] = url
				if encoded, err := json.Marshal(args); err == nil {
					tc.Input = string(encoded)
				}
			}
		}
	case "tool_call":
		if repaired, ok := repairNestedToolCallInput(tc.Input, userPrompt); ok {
			tc.Input = repaired
		}
	}

	return tc
}

func executeFantasyTool(ctx context.Context, tool fantasy.AgentTool, toolCall fantasy.ToolCallContent) (fantasy.ToolResultContent, error) {
	return executeFantasyRun(ctx, tool.Run, toolCall)
}

func executeFantasyRun(ctx context.Context, run func(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error), toolCall fantasy.ToolCallContent) (fantasy.ToolResultContent, error) {
	result := fantasy.ToolResultContent{
		ToolCallID:       toolCall.ToolCallID,
		ToolName:         toolCall.ToolName,
		ProviderExecuted: false,
	}

	response, err := run(ctx, fantasy.ToolCall{
		ID:    toolCall.ToolCallID,
		Name:  toolCall.ToolName,
		Input: toolCall.Input,
	})
	if err != nil {
		return result, err
	}

	result.ClientMetadata = response.Metadata
	result.StopTurn = response.StopTurn
	if response.IsError {
		result.Result = fantasy.ToolResultOutputContentError{Error: errors.New(response.Content)}
		return result, nil
	}

	switch response.Type {
	case "image", "media":
		result.Result = fantasy.ToolResultOutputContentMedia{
			Data:      base64.StdEncoding.EncodeToString(response.Data),
			MediaType: response.MediaType,
			Text:      response.Content,
		}
	default:
		result.Result = fantasy.ToolResultOutputContentText{Text: response.Content}
	}

	return result, nil
}

func fantasyToolMap(toolsList []fantasy.AgentTool, registry *tools.ToolRegistry) map[string]fantasy.AgentTool {
	if len(toolsList) == 0 {
		return nil
	}

	fallback := make(map[string]fantasy.AgentTool)
	for _, tool := range toolsList {
		if tool == nil {
			continue
		}
		name := tool.Info().Name
		if registry != nil {
			if _, ok := registry.Get(name); ok {
				continue
			}
		}
		fallback[name] = tool
	}
	if len(fallback) == 0 {
		return nil
	}
	return fallback
}

func repairDirectArg(input, field, replacement string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", false
	}

	current, _ := args[field].(string)
	if !isPlaceholderValue(current) {
		return "", false
	}

	args[field] = replacement
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func forceDirectArg(input, field, replacement string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", false
	}

	if stringArg(args[field]) == replacement {
		return "", false
	}

	args[field] = replacement
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func repairWriteFileInput(input, path, content string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", false
	}

	changed := false
	if path != "" && isPlaceholderValue(stringArg(args["path"])) {
		args["path"] = path
		changed = true
	}
	if content != "" && isPlaceholderValue(stringArg(args["content"])) {
		args["content"] = content
		changed = true
	}
	if !changed {
		return "", false
	}

	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func repairNestedToolCallInput(input, userPrompt string) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", false
	}

	toolName, _ := args["tool_name"].(string)
	nested, _ := args["arguments"].(map[string]any)
	if nested == nil {
		return "", false
	}

	changed := false
	switch toolName {
	case "exec":
		if command := explicitExecCommand(userPrompt); command != "" && stringArg(nested["command"]) != command {
			nested["command"] = command
			changed = true
		}
	case "skill_read":
		if skillName := explicitSkillName(userPrompt); skillName != "" && isPlaceholderValue(stringArg(nested["name"])) {
			nested["name"] = skillName
			changed = true
		}
	case "read_file":
		if path := explicitReadFilePath(userPrompt); path != "" && isPlaceholderValue(stringArg(nested["path"])) {
			nested["path"] = path
			changed = true
		}
	case "write_file":
		if path, content := explicitWriteFileRequest(userPrompt); path != "" || content != "" {
			if path != "" && isPlaceholderValue(stringArg(nested["path"])) {
				nested["path"] = path
				changed = true
			}
			if content != "" && isPlaceholderValue(stringArg(nested["content"])) {
				nested["content"] = content
				changed = true
			}
		}
	case "list_dir":
		if path := explicitListDirPath(userPrompt); path != "" && isPlaceholderValue(stringArg(nested["path"])) {
			nested["path"] = path
			changed = true
		}
	case "web_fetch":
		if url := explicitFetchURL(userPrompt); url != "" && isMalformedURLValue(stringArg(nested["url"])) {
			nested["url"] = url
			changed = true
		}
	}
	if !changed {
		return "", false
	}

	args["arguments"] = nested
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func stringArg(v any) string {
	s, _ := v.(string)
	return s
}

func isPlaceholderValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	if trimmed == ":" {
		return true
	}
	return strings.Trim(trimmed, "{}[]() \t\r\n:;,.'\"`") == ""
}

func isMalformedURLValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if isPlaceholderValue(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	return !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://")
}
