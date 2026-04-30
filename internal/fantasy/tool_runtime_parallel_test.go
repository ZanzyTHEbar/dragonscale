package fantasy

import (
	"context"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy/internal/testcmp"
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

	results, err := runtime.Execute(t.Context(), []AgentTool{tool}, nil, toolCalls, cb)
	require.NoError(t, err)
	gotResults := make([]struct {
		ToolCallID string
		Text       string
	}, 0, len(results))
	for _, result := range results {
		gotResults = append(gotResults, struct {
			ToolCallID string
			Text       string
		}{
			ToolCallID: result.ToolCallID,
			Text:       result.Result.(ToolResultOutputContentText).Text,
		})
	}
	testcmp.AssertEqual(t, []struct {
		ToolCallID string
		Text       string
	}{
		{ToolCallID: "c1", Text: "a"},
		{ToolCallID: "c2", Text: "b"},
		{ToolCallID: "c3", Text: "c"},
	}, gotResults)

	cbMu.Lock()
	testcmp.AssertEqual(t, []string{"c1", "c2", "c3"}, cbOrder)
	cbMu.Unlock()
}

func TestParallelToolRuntime_ExecutableProviderTool(t *testing.T) {
	t.Parallel()

	providerTool := NewExecutableProviderTool(
		ProviderDefinedTool{ID: "provider.local", Name: "provider_local"},
		func(_ context.Context, call ToolCall) (ToolResponse, error) {
			return NewTextResponse("ok:" + call.ID), nil
		},
	)

	results, err := ParallelToolRuntime{}.Execute(
		t.Context(),
		nil,
		[]ExecutableProviderTool{providerTool},
		[]ToolCallContent{{ToolCallID: "pt1", ToolName: "provider_local", Input: `{}`}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	testcmp.AssertEqual(t, "ok:pt1", results[0].Result.(ToolResultOutputContentText).Text)
}
