package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
)

// SecureBusToolRuntime is a fantasy.ToolRuntime that routes every tool call
// through the SecureBus before (and after) passing it to the underlying runtime.
//
// Pipeline per tool call:
//  1. Serialize tool call args → ToolRequest
//  2. bus.Execute() → capability check, secret injection, output scan, audit
//  3. If bus returns a policy error, short-circuit with that error result
//  4. Otherwise delegate to Base runtime for actual execution
//  5. If bus detected a leak, replace Base output with the redacted version
type SecureBusToolRuntime struct {
	// Base is the underlying runtime and is required.
	Base fantasy.ToolRuntime

	// Bus is required.
	Bus *securebus.Bus

	// SessionKey is forwarded to bus requests for audit tracing.
	SessionKey string

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
	tools []fantasy.AgentTool,
	toolCalls []fantasy.ToolCallContent,
	onResult func(fantasy.ToolResultContent) error,
) ([]fantasy.ToolResultContent, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	if r.Bus == nil {
		return nil, fmt.Errorf("secure bus runtime requires bus")
	}
	if r.Base == nil {
		return nil, fmt.Errorf("secure bus runtime requires base runtime")
	}

	results := make([]fantasy.ToolResultContent, 0, len(toolCalls))
	stepIndex := fantasy.StepIndexFromCtx(ctx)

	type deferredState struct {
		step     int
		state    string
		snapshot map[string]any
	}
	var pendingStates []deferredState

	for i, tc := range toolCalls {
		tc = repairToolCallInput(tc, r.UserPrompt)
		step := stepIndex
		pendingStates = append(pendingStates, deferredState{step, "tool_call", map[string]any{
			"tool_name":       tc.ToolName,
			"tool_call_index": i,
		}})

		reqID := ids.New().String()
		req := itr.NewToolExecRequest(reqID, r.SessionKey, tc.ToolCallID, tc.ToolName, tc.Input)
		busResp := r.Bus.Execute(ctx, req)

		if busResp.IsError {
			sanitized := sanitizePolicyError(busResp.Result)
			tr := fantasy.ToolResultContent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Result:     fantasy.ToolResultOutputContentError{Error: errors.New(sanitized)},
			}
			pendingStates = append(pendingStates, deferredState{step, "tool_call_error", map[string]any{
				"tool_name":  tc.ToolName,
				"error":      busResp.Result,
				"error_safe": sanitized,
			}})
			results = append(results, tr)
			if onResult != nil {
				if err := onResult(tr); err != nil {
					return results, err
				}
			}
			continue
		}

		// Execute via Base runtime for the single tool call.
		baseResults, err := r.Base.Execute(fantasy.WithStepIndex(ctx, stepIndex), tools, []fantasy.ToolCallContent{tc}, nil)
		if err != nil {
			return results, err
		}

		for _, br := range baseResults {
			if busResp.LeakDetected {
				br = overrideResultText(br, busResp.Result)
			}
			results = append(results, br)
			pendingStates = append(pendingStates, deferredState{step, "tool_result", map[string]any{
				"tool_name":       tc.ToolName,
				"tool_call_index": i,
			}})
			if onResult != nil {
				if err := onResult(br); err != nil {
					return results, err
				}
			}
		}
	}

	// Flush all buffered state writes in one pass
	for _, ps := range pendingStates {
		r.recordRunState(ctx, ps.step, ps.state, ps.snapshot)
	}

	return results, nil
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

// overrideResultText replaces the text output of a ToolResultContent with
// the redacted version produced by the SecureBus.
func overrideResultText(tr fantasy.ToolResultContent, text string) fantasy.ToolResultContent {
	if _, ok := tr.Result.(fantasy.ToolResultOutputContentText); ok {
		tr.Result = fantasy.ToolResultOutputContentText{Text: text}
	}
	return tr
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
