package fantasy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParallelToolRuntime_OrderAndCallbackDeterminism(t *testing.T) {
	t.Parallel()

	type input struct {
		DelayMs int    `json:"delay_ms"`
		Value   string `json:"value"`
	}

	tool := NewParallelAgentTool("p", "parallel tool", func(ctx context.Context, in input, _ ToolCall) (ToolResponse, error) {
		if in.DelayMs > 0 {
			select {
			case <-ctx.Done():
				return NewTextErrorResponse(ctx.Err().Error()), nil
			case <-time.After(time.Duration(in.DelayMs) * time.Millisecond):
			}
		}
		return NewTextResponse(in.Value), nil
	})

	runtime := ParallelToolRuntime{MaxConcurrency: 3}

	var cbMu sync.Mutex
	var cbOrder []string
	cb := func(res ToolResultContent) error {
		cbMu.Lock()
		defer cbMu.Unlock()
		cbOrder = append(cbOrder, res.ToolCallID)
		return nil
	}

	toolCalls := []ToolCallContent{
		{ToolCallID: "c1", ToolName: "p", Input: `{"delay_ms":50,"value":"a"}`},
		{ToolCallID: "c2", ToolName: "p", Input: `{"delay_ms":10,"value":"b"}`},
		{ToolCallID: "c3", ToolName: "p", Input: `{"delay_ms":30,"value":"c"}`},
	}

	results, err := runtime.Execute(t.Context(), []AgentTool{tool}, toolCalls, cb)
	require.NoError(t, err)
	require.Len(t, results, 3)

	require.Equal(t, "c1", results[0].ToolCallID)
	require.Equal(t, "a", results[0].Result.(ToolResultOutputContentText).Text)
	require.Equal(t, "c2", results[1].ToolCallID)
	require.Equal(t, "b", results[1].Result.(ToolResultOutputContentText).Text)
	require.Equal(t, "c3", results[2].ToolCallID)
	require.Equal(t, "c", results[2].Result.(ToolResultOutputContentText).Text)

	cbMu.Lock()
	require.Equal(t, []string{"c1", "c2", "c3"}, cbOrder)
	cbMu.Unlock()
}

func TestParallelToolRuntime_BarrierForNonParallelTools(t *testing.T) {
	t.Parallel()

	parallel := NewParallelAgentTool("p", "parallel tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		_ = call
		return NewTextResponse("p"), nil
	})
	seq := NewAgentTool("s", "sequential tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		_ = call
		return NewTextResponse("s"), nil
	})

	runtime := ParallelToolRuntime{MaxConcurrency: 8}

	var mu sync.Mutex
	var order []string
	cb := func(res ToolResultContent) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, res.ToolCallID)
		return nil
	}

	toolCalls := []ToolCallContent{
		{ToolCallID: "p1", ToolName: "p", Input: `{}`},
		{ToolCallID: "p2", ToolName: "p", Input: `{}`},
		{ToolCallID: "s1", ToolName: "s", Input: `{}`},
		{ToolCallID: "p3", ToolName: "p", Input: `{}`},
	}

	results, err := runtime.Execute(t.Context(), []AgentTool{parallel, seq}, toolCalls, cb)
	require.NoError(t, err)
	require.Len(t, results, 4)

	mu.Lock()
	require.Equal(t, []string{"p1", "p2", "s1", "p3"}, order)
	mu.Unlock()
}

func TestParallelToolRuntime_CriticalErrorPropagation(t *testing.T) {
	t.Parallel()

	tool := NewParallelAgentTool("p", "parallel tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		if call.ID == "bad" {
			return ToolResponse{}, errors.New("boom")
		}
		return NewTextResponse("ok"), nil
	})

	runtime := ParallelToolRuntime{MaxConcurrency: 4}

	toolCalls := []ToolCallContent{
		{ToolCallID: "good", ToolName: "p", Input: `{}`},
		{ToolCallID: "bad", ToolName: "p", Input: `{}`},
	}

	results, err := runtime.Execute(t.Context(), []AgentTool{tool}, toolCalls, nil)
	require.Error(t, err)
	require.Nil(t, results)
}

func TestParallelToolRuntime_MetricsAndLogHooks(t *testing.T) {
	t.Parallel()

	var metricsCalled bool
	var logCalled bool

	tool := NewParallelAgentTool("p", "tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		return NewTextResponse(call.ID), nil
	})

	rt := ParallelToolRuntime{
		MaxConcurrency: 2,
		Metrics: func(m ToolRuntimeMetrics) {
			metricsCalled = true
			require.GreaterOrEqual(t, m.Queued, 0)
		},
		Log: func(e ToolRuntimeLogEvent) {
			logCalled = true
			require.NotEmpty(t, e.Event)
		},
	}

	toolCalls := []ToolCallContent{
		{ToolCallID: "a", ToolName: "p", Input: `{}`},
	}
	res, err := rt.Execute(t.Context(), []AgentTool{tool}, toolCalls, nil)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.True(t, metricsCalled)
	require.True(t, logCalled)
}
