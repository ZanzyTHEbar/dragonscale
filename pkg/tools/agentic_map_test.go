package tools

import (
	"context"
	"fmt"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
)

func TestAgenticMapTool_Execute_Success(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, userPrompt, _, _ string) (*ToolLoopResult, error) {
		return &ToolLoopResult{Content: "processed: " + userPrompt, Iterations: 1}, nil
	})

	tool := NewAgenticMapTool(manager)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"name": "a"},
			map[string]interface{}{"name": "b"},
		},
		"task_template": "Handle item {{index}} => {{item_json}}",
		"max_retries":   float64(1),
	})

	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)

	var payload struct {
		Count   int `json:"count"`
		Summary struct {
			SuccessCount int `json:"success_count"`
			FailureCount int `json:"failure_count"`
		} `json:"summary"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &payload))
	assert.Empty(t, cmp.Diff(2, payload.Count))
	assert.Empty(t, cmp.Diff(2, payload.Summary.SuccessCount))
	assert.Empty(t, cmp.Diff(0, payload.Summary.FailureCount))
}

func TestAgenticMapTool_Execute_RetriesFailedItems(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	callCount := 0
	manager.SetRunLoop(func(_ context.Context, _ ToolLoopConfig, _, userPrompt, _, _ string) (*ToolLoopResult, error) {
		callCount++
		if callCount == 1 {
			return nil, fmt.Errorf("transient failure for %s", userPrompt)
		}
		return &ToolLoopResult{Content: "processed on retry", Iterations: 1}, nil
	})

	tool := NewAgenticMapTool(manager)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"items":         []interface{}{map[string]interface{}{"name": "retry-me"}},
		"task_template": "Retry item {{index}} => {{item_json}}",
		"max_retries":   float64(2),
	})

	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)

	var payload struct {
		Results []struct {
			Success  bool `json:"success"`
			Attempts int  `json:"attempts"`
		} `json:"results"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &payload))
	require.Len(t, payload.Results, 1)
	assert.True(t, payload.Results[0].Success)
	assert.Empty(t, cmp.Diff(2, payload.Results[0].Attempts))
}

func TestAgenticMapTool_Execute_RequiresPlaceholders(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	tool := NewAgenticMapTool(manager)

	result := tool.Execute(t.Context(), map[string]interface{}{
		"items":         []interface{}{map[string]interface{}{"name": "x"}},
		"task_template": "Handle item without placeholders",
	})

	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "placeholders")
}

func TestAgenticMapTool_Execute_ContextCancelled(t *testing.T) {
	t.Parallel()
	provider := newPromptEchoLanguageModel(t)
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", bus.NewMessageBus())
	tool := NewAgenticMapTool(manager)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	result := tool.Execute(ctx, map[string]interface{}{
		"items":         []interface{}{map[string]interface{}{"name": "x"}},
		"task_template": "Handle {{index}} => {{item_json}}",
	})

	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "cancelled")
}
