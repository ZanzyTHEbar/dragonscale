package fantasy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingToolRuntime struct {
	stepIndices []int
}

func (r *recordingToolRuntime) Execute(ctx context.Context, _ []AgentTool, _ []ExecutableProviderTool, calls []ToolCallContent, _ func(ToolResultContent) error) ([]ToolResultContent, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	r.stepIndices = append(r.stepIndices, StepIndexFromCtx(ctx))
	results := make([]ToolResultContent, 0, len(calls))
	for _, call := range calls {
		results = append(results, ToolResultContent{
			ToolCallID: call.ToolCallID,
			ToolName:   call.ToolName,
			Result:     ToolResultOutputContentText{Text: "ok:" + call.ToolCallID},
		})
	}
	return results, nil
}

type multiStepToolModel struct{}

func (m *multiStepToolModel) Generate(_ context.Context, call Call) (*Response, error) {
	toolResults := countToolResults(call.Prompt)
	switch toolResults {
	case 0:
		return toolCallResponse("call-1"), nil
	case 1:
		return toolCallResponse("call-2"), nil
	default:
		return &Response{Content: ResponseContent{TextContent{Text: "final answer"}}, FinishReason: FinishReasonStop}, nil
	}
}

func (m *multiStepToolModel) Stream(ctx context.Context, call Call) (StreamResponse, error) {
	resp, err := m.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return func(yield func(StreamPart) bool) {
		if len(resp.Content.ToolCalls()) > 0 {
			for _, tc := range resp.Content.ToolCalls() {
				if !yield(StreamPart{Type: StreamPartTypeToolCall, ID: tc.ToolCallID, ToolCallName: tc.ToolName, ToolCallInput: tc.Input}) {
					return
				}
			}
			yield(StreamPart{Type: StreamPartTypeFinish, FinishReason: FinishReasonToolCalls})
			return
		}

		text := resp.Content.Text()
		if !yield(StreamPart{Type: StreamPartTypeTextStart, ID: "text-0"}) {
			return
		}
		if !yield(StreamPart{Type: StreamPartTypeTextDelta, ID: "text-0", Delta: text}) {
			return
		}
		if !yield(StreamPart{Type: StreamPartTypeTextEnd, ID: "text-0"}) {
			return
		}
		yield(StreamPart{Type: StreamPartTypeFinish, FinishReason: FinishReasonStop})
	}, nil
}

func (m *multiStepToolModel) GenerateObject(_ context.Context, _ ObjectCall) (*ObjectResponse, error) {
	return nil, nil
}

func (m *multiStepToolModel) StreamObject(_ context.Context, _ ObjectCall) (ObjectStreamResponse, error) {
	return nil, nil
}

func (m *multiStepToolModel) Provider() string { return "mock" }

func (m *multiStepToolModel) Model() string { return "multi-step-tool-model" }

func toolCallResponse(id string) *Response {
	return &Response{
		Content:      ResponseContent{ToolCallContent{ToolCallID: id, ToolName: "echo", Input: `{}`}},
		FinishReason: FinishReasonToolCalls,
	}
}

func countToolResults(prompt []Message) int {
	count := 0
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if part.GetType() == ContentTypeToolResult {
				count++
			}
		}
	}
	return count
}

func uniqueTransitionStepIndices(transitions []ReActTransition) []int {
	seen := make(map[int]struct{})
	order := make([]int, 0, len(transitions))
	for _, transition := range transitions {
		if _, ok := seen[transition.StepIndex]; ok {
			continue
		}
		seen[transition.StepIndex] = struct{}{}
		order = append(order, transition.StepIndex)
	}
	return order
}

func TestAgent_Generate_PropagatesCurrentStepIndex(t *testing.T) {
	t.Parallel()

	runtime := &recordingToolRuntime{}
	observer := &captureObserver{}
	agent := NewAgent(
		&multiStepToolModel{},
		WithTools(&mockTool{name: "echo", description: "echo", parameters: map[string]any{"type": "object"}}),
		WithToolRuntime(runtime),
		WithTransitionObserver(observer),
	)

	result, err := agent.Generate(t.Context(), AgentCall{Prompt: "run"})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []int{0, 1}, runtime.stepIndices)
	assert.Equal(t, []int{0, 1, 2}, uniqueTransitionStepIndices(observer.Snapshot()))
}

func TestAgent_Stream_PropagatesCurrentStepIndex(t *testing.T) {
	t.Parallel()

	runtime := &recordingToolRuntime{}
	observer := &captureObserver{}
	agent := NewAgent(
		&multiStepToolModel{},
		WithTools(&mockTool{name: "echo", description: "echo", parameters: map[string]any{"type": "object"}}),
		WithToolRuntime(runtime),
		WithTransitionObserver(observer),
	)

	result, err := agent.Stream(t.Context(), AgentStreamCall{Prompt: "run"})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []int{0, 1}, runtime.stepIndices)
	assert.Equal(t, []int{0, 1, 2}, uniqueTransitionStepIndices(observer.Snapshot()))
}

func TestAgent_RegularToolUsesFullSchemaValidation(t *testing.T) {
	t.Parallel()

	var called bool
	model := &mockLanguageModel{generateFunc: func(_ context.Context, call Call) (*Response, error) {
		if countToolResults(call.Prompt) > 0 {
			return &Response{Content: ResponseContent{TextContent{Text: "done"}}, FinishReason: FinishReasonStop}, nil
		}
		return &Response{
			Content:      ResponseContent{ToolCallContent{ToolCallID: "bad", ToolName: "typed", Input: `{"count":"not-a-number"}`}},
			FinishReason: FinishReasonToolCalls,
		}, nil
	}}
	tool := &mockTool{
		name:        "typed",
		description: "typed tool",
		parameters:  map[string]any{"count": map[string]any{"type": "number"}},
		required:    []string{"count"},
		executeFunc: func(_ context.Context, _ ToolCall) (ToolResponse, error) {
			called = true
			return NewTextResponse("should not run"), nil
		},
	}

	result, err := NewAgent(model, WithTools(tool)).Generate(t.Context(), AgentCall{Prompt: "run"})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Steps)
	assert.False(t, called)

	var sawValidationError bool
	for _, content := range result.Steps[0].Content {
		if tr, ok := AsContentType[ToolResultContent](content); ok {
			_, sawValidationError = tr.Result.(ToolResultOutputContentError)
		}
	}
	assert.True(t, sawValidationError)
}

func TestAgent_ActiveToolsFiltersExecution(t *testing.T) {
	t.Parallel()

	var called bool
	model := &mockLanguageModel{generateFunc: func(_ context.Context, call Call) (*Response, error) {
		if countToolResults(call.Prompt) > 0 {
			return &Response{Content: ResponseContent{TextContent{Text: "done"}}, FinishReason: FinishReasonStop}, nil
		}
		return &Response{
			Content:      ResponseContent{ToolCallContent{ToolCallID: "blocked-call", ToolName: "blocked", Input: `{}`}},
			FinishReason: FinishReasonToolCalls,
		}, nil
	}}
	blockedTool := &mockTool{
		name:        "blocked",
		description: "blocked tool",
		parameters:  map[string]any{},
		executeFunc: func(_ context.Context, _ ToolCall) (ToolResponse, error) {
			called = true
			return NewTextResponse("should not run"), nil
		},
	}
	allowedTool := &mockTool{name: "allowed", description: "allowed tool", parameters: map[string]any{}}

	result, err := NewAgent(model, WithTools(blockedTool, allowedTool)).Generate(t.Context(), AgentCall{
		Prompt:      "run",
		ActiveTools: []string{"allowed"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, called)
}
