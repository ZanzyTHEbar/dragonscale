package agent

import (
	"context"
	"errors"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/security/securebus"
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
	// Base is the underlying runtime. If nil, the bus result is used directly.
	Base fantasy.ToolRuntime

	// Bus is required.
	Bus *securebus.Bus

	// SessionKey is forwarded to bus requests for audit tracing.
	SessionKey string
}

// Execute implements fantasy.ToolRuntime.
func (r SecureBusToolRuntime) Execute(
	ctx context.Context,
	tools []fantasy.AgentTool,
	toolCalls []fantasy.ToolCallContent,
	onResult func(fantasy.ToolResultContent) error,
) ([]fantasy.ToolResultContent, error) {
	if r.Bus == nil || len(toolCalls) == 0 {
		if r.Base != nil {
			return r.Base.Execute(ctx, tools, toolCalls, onResult)
		}
		return nil, nil
	}

	results := make([]fantasy.ToolResultContent, 0, len(toolCalls))

	for _, tc := range toolCalls {
		reqID := ids.New().String()
		req := itr.NewToolExecRequest(reqID, r.SessionKey, tc.ToolCallID, tc.ToolName, tc.Input)
		busResp := r.Bus.Execute(ctx, req)

		if busResp.IsError {
			// Policy violation or secret resolution failure.
			tr := fantasy.ToolResultContent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Result:     fantasy.ToolResultOutputContentError{Error: errors.New(busResp.Result)},
			}
			results = append(results, tr)
			if onResult != nil {
				if err := onResult(tr); err != nil {
					return results, err
				}
			}
			continue
		}

		// Bus accepted — delegate to Base for actual execution.
		if r.Base == nil {
			tr := fantasy.ToolResultContent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Result:     fantasy.ToolResultOutputContentText{Text: busResp.Result},
			}
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
			if onResult != nil {
				if err := onResult(br); err != nil {
					return results, err
				}
			}
		}
	}

	return results, nil
}

// overrideResultText replaces the text output of a ToolResultContent with
// the redacted version produced by the SecureBus.
func overrideResultText(tr fantasy.ToolResultContent, text string) fantasy.ToolResultContent {
	if _, ok := tr.Result.(fantasy.ToolResultOutputContentText); ok {
		tr.Result = fantasy.ToolResultOutputContentText{Text: text}
	}
	return tr
}
