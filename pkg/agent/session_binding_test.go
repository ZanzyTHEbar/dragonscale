package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"

	pkgroot "github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextBuilder_UsesActiveSessionWorkingContext(t *testing.T) {
	t.Parallel()

	al := newSessionBoundAgentLoop(t)
	require.NoError(t, al.memoryStore.SetWorkingContext(t.Context(), pkgroot.NAME, "session-a", "working context A"))
	require.NoError(t, al.memoryStore.SetWorkingContext(t.Context(), pkgroot.NAME, "session-b", "working context B"))

	al.activeSessionKey.Store("session-a")
	promptA := al.contextBuilder.BuildSystemPromptWithBudget(0)
	assert.Contains(t, promptA, "working context A")
	assert.NotContains(t, promptA, "working context B")

	al.activeSessionKey.Store("session-b")
	promptB := al.contextBuilder.BuildSystemPromptWithBudget(0)
	assert.Contains(t, promptB, "working context B")
	assert.NotContains(t, promptB, "working context A")
}

func TestMemGPTTool_UsesResolvedSessionForWriteAndStatus(t *testing.T) {
	t.Parallel()

	al := newSessionBoundAgentLoop(t)
	toolAny, ok := al.tools.Get("memory")
	require.True(t, ok)
	memTool, ok := toolAny.(*MemGPTTool)
	require.True(t, ok)

	al.activeSessionKey.Store("session-a")
	writeA := memTool.Execute(t.Context(), map[string]interface{}{
		"action":  "write",
		"content": "session-scoped memory",
		"tier":    "recall",
		"sector":  "semantic",
	})
	require.False(t, writeA.IsError)

	al.activeSessionKey.Store("session-b")
	writeB := memTool.Execute(t.Context(), map[string]interface{}{
		"action":  "write",
		"content": "session-scoped memory",
		"tier":    "recall",
		"sector":  "semantic",
	})
	require.False(t, writeB.IsError)

	sessionAItems, err := al.memDelegate.ListRecallItems(t.Context(), pkgroot.NAME, "session-a", 10, 0)
	require.NoError(t, err)
	sessionBItems, err := al.memDelegate.ListRecallItems(t.Context(), pkgroot.NAME, "session-b", 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, sessionAItems)
	require.NotEmpty(t, sessionBItems)
	assert.Equal(t, "session-scoped memory", sessionAItems[0].Content)
	assert.Equal(t, "session-scoped memory", sessionBItems[0].Content)

	require.NoError(t, al.memoryStore.SetWorkingContext(t.Context(), pkgroot.NAME, "session-a", "scoped working context"))

	al.activeSessionKey.Store("session-a")
	statusA := invokeMemoryAction(t, memTool, map[string]interface{}{"action": "status"})
	require.NotNil(t, statusA.Status)
	assert.Greater(t, statusA.Status.WorkingContextTokens, 0)
	assert.Equal(t, 1, statusA.Status.RecallItemCount)

	al.activeSessionKey.Store("session-b")
	statusB := invokeMemoryAction(t, memTool, map[string]interface{}{"action": "status"})
	require.NotNil(t, statusB.Status)
	assert.Equal(t, 0, statusB.Status.WorkingContextTokens)
	assert.Equal(t, 1, statusB.Status.RecallItemCount)
}

func newSessionBoundAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "session-binding-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Memory.DBPath = filepath.Join(tmpDir, "session-binding.db")

	return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("ok"))
}

func invokeMemoryAction(t *testing.T, tool *MemGPTTool, args map[string]interface{}) memstore.MemoryToolResponse {
	t.Helper()

	result := tool.Execute(t.Context(), args)
	require.False(t, result.IsError)

	var response memstore.MemoryToolResponse
	require.NoError(t, jsonv2.Unmarshal([]byte(result.ForLLM), &response))
	require.True(t, response.Success)
	return response
}

func TestMemGPTTool_PrefersContextSessionOverActiveSession(t *testing.T) {
	t.Parallel()

	al := newSessionBoundAgentLoop(t)
	toolAny, ok := al.tools.Get("memory")
	require.True(t, ok)
	memTool, ok := toolAny.(*MemGPTTool)
	require.True(t, ok)

	al.activeSessionKey.Store("session-b")
	ctx := tools.WithSessionKey(context.Background(), "session-a")
	writeA := memTool.Execute(ctx, map[string]interface{}{
		"action":  "write",
		"content": "context-bound memory",
		"tier":    "recall",
		"sector":  "semantic",
	})
	require.False(t, writeA.IsError)

	sessionAItems, err := al.memDelegate.ListRecallItems(t.Context(), pkgroot.NAME, "session-a", 10, 0)
	require.NoError(t, err)
	sessionBItems, err := al.memDelegate.ListRecallItems(t.Context(), pkgroot.NAME, "session-b", 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, sessionAItems)
	assert.Equal(t, "context-bound memory", sessionAItems[0].Content)
	assert.Empty(t, sessionBItems)
}
