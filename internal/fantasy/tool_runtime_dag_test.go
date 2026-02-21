package fantasy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDAGToolRuntime_IndependentToolsRunConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	release := make(chan struct{})

	tool := NewParallelAgentTool("p", "parallel tool", func(ctx context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		select {
		case started <- call.ID:
		default:
		}
		select {
		case <-ctx.Done():
			return NewTextErrorResponse(ctx.Err().Error()), nil
		case <-release:
		}
		return NewTextResponse(call.ID), nil
	})

	rt := DAGToolRuntime{MaxConcurrency: 2}

	toolCalls := []ToolCallContent{
		{ToolCallID: "a", ToolName: "p", Input: `{}`},
		{ToolCallID: "b", ToolName: "p", Input: `{}`},
	}

	var (
		res []ToolResultContent
		err error
		wg  sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err = rt.Execute(t.Context(), []AgentTool{tool}, toolCalls, nil)
	}()

	// Both tools should start before we release.
	got1 := <-started
	got2 := <-started
	require.NotEqual(t, got1, got2)

	close(release)
	wg.Wait()

	require.NoError(t, err)
	require.Len(t, res, 2)
}

func TestDAGToolRuntime_DependenciesWaitAndInputIsResolved(t *testing.T) {
	t.Parallel()

	startA := make(chan struct{}, 1)
	startB := make(chan struct{}, 1)
	releaseA := make(chan struct{})

	toolA := NewParallelAgentTool("a", "tool a", func(ctx context.Context, _ struct{}, _ ToolCall) (ToolResponse, error) {
		startA <- struct{}{}
		select {
		case <-ctx.Done():
			return NewTextErrorResponse(ctx.Err().Error()), nil
		case <-releaseA:
		}
		// JSON output that downstream can path into.
		return NewTextResponse(`{"x":1}`), nil
	})

	type bInput struct {
		Val int `json:"val"`
	}
	toolB := NewParallelAgentTool("b", "tool b", func(_ context.Context, in bInput, _ ToolCall) (ToolResponse, error) {
		startB <- struct{}{}
		return NewTextResponse(string(rune('0' + in.Val))), nil
	})

	rt := DAGToolRuntime{MaxConcurrency: 4}
	toolCalls := []ToolCallContent{
		{ToolCallID: "callA", ToolName: "a", Input: `{}`},
		{ToolCallID: "callB", ToolName: "b", Input: `{"val":"$tool.callA.x"}`},
	}

	var (
		res []ToolResultContent
		err error
		wg  sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err = rt.Execute(t.Context(), []AgentTool{toolA, toolB}, toolCalls, nil)
	}()

	<-startA

	// B must not start until A is released.
	select {
	case <-startB:
		t.Fatalf("dependent tool started before dependency completed")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseA)

	<-startB
	wg.Wait()

	require.NoError(t, err)
	require.Len(t, res, 2)

	// B should have received val=1 and returned "1".
	require.Equal(t, "callB", res[1].ToolCallID)
	require.Equal(t, "1", res[1].Result.(ToolResultOutputContentText).Text)
}

func TestDAGToolRuntime_CycleDetected(t *testing.T) {
	t.Parallel()

	tool := NewParallelAgentTool("p", "tool", func(_ context.Context, _ struct{}, _ ToolCall) (ToolResponse, error) {
		return NewTextResponse("ok"), nil
	})

	rt := DAGToolRuntime{MaxConcurrency: 4}
	toolCalls := []ToolCallContent{
		{ToolCallID: "a", ToolName: "p", Input: `{"x":"$tool.b"}`},
		{ToolCallID: "b", ToolName: "p", Input: `{"x":"$tool.a"}`},
	}

	res, err := rt.Execute(t.Context(), []AgentTool{tool}, toolCalls, nil)
	require.Error(t, err)
	require.Nil(t, res)
}

func TestDAGToolRuntime_OnToolResultSerialized(t *testing.T) {
	t.Parallel()

	var inFlight atomic.Int32
	var orderMu sync.Mutex
	var order []string

	tool := NewParallelAgentTool("p", "tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		return NewTextResponse(call.ID), nil
	})

	toolCalls := []ToolCallContent{
		{ToolCallID: "a", ToolName: "p", Input: `{}`},
		{ToolCallID: "b", ToolName: "p", Input: `{}`},
	}

	rt := DAGToolRuntime{MaxConcurrency: 2}
	cb := func(res ToolResultContent) error {
		if inFlight.Add(1) != 1 {
			return fmt.Errorf("callback executed concurrently")
		}
		orderMu.Lock()
		order = append(order, res.ToolCallID)
		orderMu.Unlock()
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return nil
	}

	res, err := rt.Execute(t.Context(), []AgentTool{tool}, toolCalls, cb)
	require.NoError(t, err)
	require.Len(t, res, 2)

	orderMu.Lock()
	defer orderMu.Unlock()
	require.Equal(t, []string{"a", "b"}, order)
}

func TestDAGToolRuntime_MetricsAndLogHooks(t *testing.T) {
	t.Parallel()

	var metricsCalled bool
	var logCalled bool

	tool := NewParallelAgentTool("p", "tool", func(_ context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		return NewTextResponse(call.ID), nil
	})

	rt := DAGToolRuntime{
		MaxConcurrency: 1,
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
