package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
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

	// Optional state persistence for runtime execution.
	StateStore *StateStore
	RunID      ids.UUID
	StepIndex  int
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

	type deferredState struct {
		step     int
		state    string
		snapshot map[string]any
	}
	var pendingStates []deferredState

	for i, tc := range toolCalls {
		step := r.StepIndex + i
		pendingStates = append(pendingStates, deferredState{step, "tool_call", map[string]any{
			"tool_name": tc.ToolName,
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
		baseResults, err := r.Base.Execute(ctx, tools, []fantasy.ToolCallContent{tc}, nil)
		if err != nil {
			return results, err
		}

		for _, br := range baseResults {
			if busResp.LeakDetected {
				br = overrideResultText(br, busResp.Result)
			}
			results = append(results, br)
			pendingStates = append(pendingStates, deferredState{step, "tool_result", map[string]any{
				"tool_name": tc.ToolName,
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
	_, _ = r.StateStore.AddRunState(ctx, r.RunID, stepIndex, fantasy.ReActState(state), snapshot)
}

// sanitizePolicyError strips internal details from policy/bus errors before
// they reach the LLM. The full error is preserved in audit state only.
func sanitizePolicyError(raw string) string {
	switch {
	case strings.Contains(raw, "recursion depth"):
		return "policy violation: recursion limit exceeded"
	case strings.Contains(raw, "network access denied"):
		return "policy violation: network access denied"
	case strings.Contains(raw, "filesystem access denied"):
		return "policy violation: filesystem access denied"
	case strings.Contains(raw, "secret injection failed"):
		return "policy violation: unable to resolve required secrets"
	case strings.Contains(raw, "invalid args JSON"):
		return "policy violation: invalid tool arguments"
	case strings.Contains(raw, "policy violation"):
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
