package fantasy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGToolRuntime_IndependentToolsRunConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	release := make(chan struct{})
	tool := NewParallelAgentTool("p", "parallel tool", func(ctx context.Context, _ struct{}, call ToolCall) (ToolResponse, error) {
		started <- call.ID
		select {
		case <-ctx.Done():
			return NewTextErrorResponse(ctx.Err().Error()), nil
		case <-release:
		}
		return NewTextResponse(call.ID), nil
	})

	var (
		res []ToolResultContent
		err error
		wg  sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err = DAGToolRuntime{MaxConcurrency: 2}.Execute(t.Context(), []AgentTool{tool}, nil, []ToolCallContent{
			{ToolCallID: "a", ToolName: "p", Input: `{}`},
			{ToolCallID: "b", ToolName: "p", Input: `{}`},
		}, nil)
	}()

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
		return NewTextResponse(`{"x":1}`), nil
	})

	type bInput struct {
		Val int `json:"val"`
	}
	toolB := NewParallelAgentTool("b", "tool b", func(_ context.Context, in bInput, _ ToolCall) (ToolResponse, error) {
		startB <- struct{}{}
		return NewTextResponse(string(rune('0' + in.Val))), nil
	})

	var (
		res []ToolResultContent
		err error
		wg  sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err = DAGToolRuntime{MaxConcurrency: 4}.Execute(t.Context(), []AgentTool{toolA, toolB}, nil, []ToolCallContent{
			{ToolCallID: "callA", ToolName: "a", Input: `{}`},
			{ToolCallID: "callB", ToolName: "b", Input: `{"val":"$tool.callA.x"}`},
		}, nil)
	}()

	<-startA
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
	assert.Equal(t, "callB", res[1].ToolCallID)
	assert.Equal(t, "1", res[1].Result.(ToolResultOutputContentText).Text)
}
