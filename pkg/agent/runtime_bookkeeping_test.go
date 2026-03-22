package agent

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	fantasy "charm.land/fantasy"
	pkgroot "github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type multiToolCallingModel struct{}

func (m *multiToolCallingModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	toolResults := countPromptToolResults(call.Prompt)
	switch toolResults {
	case 0:
		return &fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"one"}`},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
		}, nil
	case 1:
		return &fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "call-2", ToolName: "echo", Input: `{"text":"two"}`},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 6, OutputTokens: 3, TotalTokens: 9},
		}, nil
	default:
		return &fantasy.Response{
			Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "Final response after two tools"}},
			FinishReason: fantasy.FinishReasonStop,
			Usage:        fantasy.Usage{InputTokens: 7, OutputTokens: 5, TotalTokens: 12},
		}, nil
	}
}

func (m *multiToolCallingModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	resp, err := m.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if len(resp.Content.ToolCalls()) > 0 {
			for _, tc := range resp.Content.ToolCalls() {
				if !yield(fantasy.StreamPart{
					Type:          fantasy.StreamPartTypeToolCall,
					ID:            tc.ToolCallID,
					ToolCallName:  tc.ToolName,
					ToolCallInput: tc.Input,
				}) {
					return
				}
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: resp.Usage})
			return
		}

		text := resp.Content.Text()
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text-0"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text-0", Delta: text}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text-0"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: resp.Usage})
	}, nil
}

func (m *multiToolCallingModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *multiToolCallingModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *multiToolCallingModel) Provider() string { return "mock" }

func (m *multiToolCallingModel) Model() string { return "multi-tool-model" }

type sameStepMultiToolModel struct{}

func (m *sameStepMultiToolModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	if countPromptToolResults(call.Prompt) == 0 {
		return &fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "call-1", ToolName: "echo", Input: `{"text":"one"}`},
				fantasy.ToolCallContent{ToolCallID: "call-2", ToolName: "echo", Input: `{"text":"two"}`},
			},
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 5, OutputTokens: 4, TotalTokens: 9},
		}, nil
	}
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "Final response after parallel tools"}},
		FinishReason: fantasy.FinishReasonStop,
		Usage:        fantasy.Usage{InputTokens: 6, OutputTokens: 5, TotalTokens: 11},
	}, nil
}

func (m *sameStepMultiToolModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	resp, err := m.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if len(resp.Content.ToolCalls()) > 0 {
			for _, tc := range resp.Content.ToolCalls() {
				if !yield(fantasy.StreamPart{
					Type:          fantasy.StreamPartTypeToolCall,
					ID:            tc.ToolCallID,
					ToolCallName:  tc.ToolName,
					ToolCallInput: tc.Input,
				}) {
					return
				}
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: resp.Usage})
			return
		}
		text := resp.Content.Text()
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text-0"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text-0", Delta: text}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text-0"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: resp.Usage})
	}, nil
}

func (m *sameStepMultiToolModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *sameStepMultiToolModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *sameStepMultiToolModel) Provider() string { return "mock" }

func (m *sameStepMultiToolModel) Model() string { return "same-step-multi-tool-model" }

func countPromptToolResults(prompt []fantasy.Message) int {
	count := 0
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if part.GetType() == fantasy.ContentTypeToolResult {
				count++
			}
		}
	}
	return count
}

func uniqueTransitionSteps(rows []memsqlc.AgentStateTransition) []int64 {
	seen := make(map[int64]struct{})
	steps := make([]int64, 0, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.StepIndex]; ok {
			continue
		}
		seen[row.StepIndex] = struct{}{}
		steps = append(steps, row.StepIndex)
	}
	return steps
}

func TestIntegration_RuntimeBookkeeping_PersistsTransitionsAndMetrics(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-runtime-bookkeeping-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Sandbox:           tmpDir,
				Model:             "multi-tool-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &multiToolCallingModel{})
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	since := time.Now().Add(-time.Second)
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "Use the echo tool twice",
		SessionKey: "runtime-bookkeeping-session",
	}

	response, err := al.processMessage(ctx, msg)
	require.NoError(t, err)
	assert.Contains(t, response, "Final response after two tools")

	completions, err := al.queries.GetCompletedTasks(ctx, memsqlc.GetCompletedTasksParams{
		AgentID: pkgroot.NAME,
		Since:   since,
	})
	require.NoError(t, err)
	require.NotEmpty(t, completions)

	completion := completions[len(completions)-1]
	require.NotNil(t, completion.ToolCalls)
	assert.Equal(t, int64(2), *completion.ToolCalls)

	transitions, err := al.queries.ListAgentStateTransitionsByRunID(ctx, memsqlc.ListAgentStateTransitionsByRunIDParams{
		RunID: completion.RunID,
		Lim:   128,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, transitions)
	assert.Equal(t, []int64{0, 1, 2}, uniqueTransitionSteps(transitions))

	toolResults, err := al.queries.ListAgentToolResultsByRunID(ctx, memsqlc.ListAgentToolResultsByRunIDParams{
		RunID: completion.RunID,
		Lim:   16,
	})
	require.NoError(t, err)
	require.Len(t, toolResults, 2)
	assert.Equal(t, int64(0), toolResults[0].StepIndex)
	assert.Equal(t, int64(1), toolResults[1].StepIndex)
}

func TestIntegration_RuntimeBookkeeping_UsesAgentStepForMultipleToolCalls(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-runtime-multicall-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Sandbox:           tmpDir,
				Model:             "same-step-multi-tool-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &sameStepMultiToolModel{})
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	since := time.Now().Add(-time.Second)
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "Use two echo tool calls in one step",
		SessionKey: "runtime-bookkeeping-multicall",
	}

	response, err := al.processMessage(ctx, msg)
	require.NoError(t, err)
	assert.Contains(t, response, "Final response after parallel tools")

	completions, err := al.queries.GetCompletedTasks(ctx, memsqlc.GetCompletedTasksParams{
		AgentID: pkgroot.NAME,
		Since:   since,
	})
	require.NoError(t, err)
	require.NotEmpty(t, completions)

	completion := completions[len(completions)-1]
	require.NotNil(t, completion.ToolCalls)
	assert.Equal(t, int64(2), *completion.ToolCalls)

	toolResults, err := al.queries.ListAgentToolResultsByRunID(ctx, memsqlc.ListAgentToolResultsByRunIDParams{
		RunID: completion.RunID,
		Lim:   16,
	})
	require.NoError(t, err)
	require.Len(t, toolResults, 2)
	assert.Equal(t, int64(0), toolResults[0].StepIndex)
	assert.Equal(t, int64(0), toolResults[1].StepIndex)
}
