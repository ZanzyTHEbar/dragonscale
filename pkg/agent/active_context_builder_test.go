package agent

import (
	"os"
	"path/filepath"
	"testing"

	pkgroot "github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleContext_UsesActiveContextProjection(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "active-context-projection-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Memory.DBPath = filepath.Join(tmpDir, "active-context.db")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("ok"))
	require.NotNil(t, al.activeContextBuilder)

	sessionKey := "projection-session"
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "user",
		Content:       "How does the runtime work?",
		TokenEstimate: 16,
	})
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "assistant",
		Content:       "It runs a ReAct loop with persisted state.",
		TokenEstimate: 18,
	})
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "tool",
		Content:       "{\"result\":\"stored\"}",
		ToolCallID:    "tool-1",
		TokenEstimate: 12,
	})

	ac, err := al.assembleContext(t.Context(), processOptions{
		SessionKey:    sessionKey,
		UserMessage:   "Summarize the prior tool result",
		EnableSummary: false,
	})
	require.NoError(t, err)
	require.NotNil(t, ac.projection)

	assert.True(t, ac.projection.HasLosslessRefs())
	assert.NotEmpty(t, ac.fantasyHistory)
	assert.Len(t, ac.fantasyHistory, 3)
	assert.Contains(t, projectionKinds(ac.projection), memory.ProjectionSegmentSystem)
	assert.Contains(t, projectionKinds(ac.projection), memory.ProjectionSegmentRecent)
	assert.Contains(t, projectionKinds(ac.projection), memory.ProjectionSegmentTool)
	assert.Contains(t, ac.systemPrompt, "Plans vs actions")
	assert.Contains(t, ac.systemPrompt, "Direct tool routing")
	assert.NotContains(t, ac.systemPrompt, "You have access to the following tools")
}

func TestActiveContextBuilder_IncludesPersistedDAGProjection(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "active-context-dag-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Memory.DBPath = filepath.Join(tmpDir, "active-context-dag.db")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("ok"))
	require.NotNil(t, al.activeContextBuilder)

	sessionKey := "dag-projection-session"
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "user",
		Content:       "one",
		TokenEstimate: 4,
	})
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "assistant",
		Content:       "two",
		TokenEstimate: 4,
	})
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "user",
		Content:       "three",
		TokenEstimate: 4,
	})
	insertImmutableMessage(t, al, &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          "assistant",
		Content:       "four",
		TokenEstimate: 4,
	})

	persister, ok := al.memDelegate.(dag.DAGPersister)
	require.True(t, ok)

	tree := dag.NewDAG()
	tree.Add(&dag.Node{
		ID:       "session-root",
		Level:    dag.LevelSession,
		Summary:  "compressed session summary",
		Tokens:   24,
		StartIdx: 0,
		EndIdx:   4,
	})
	tree.SetRoots([]string{"session-root"})
	require.NoError(t, persister.PersistDAG(t.Context(), pkgroot.NAME, sessionKey, &dag.PersistSnapshot{
		FromMsgIdx: 0,
		ToMsgIdx:   4,
		MsgCount:   4,
		DAG:        tree,
	}))

	built, err := al.activeContextBuilder.BuildTurnContext(t.Context(), TurnContextBuildRequest{
		ProjectionRequest: memory.ProjectionRequest{
			AgentID:    pkgroot.NAME,
			SessionKey: sessionKey,
			MaxTokens:  4096,
		},
		CurrentMessage: "summarize",
	})
	require.NoError(t, err)
	require.NotNil(t, built.Projection)
	assert.Contains(t, projectionKinds(built.Projection), memory.ProjectionSegmentDAG)
}

func TestActiveContextBuilder_UsesTurnSpecificToolHintsInSystemSegment(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "active-context-tools-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Memory.DBPath = filepath.Join(tmpDir, "active-context-tools.db")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("ok"))
	require.NotNil(t, al.activeContextBuilder)

	built, err := al.activeContextBuilder.BuildTurnContext(t.Context(), TurnContextBuildRequest{
		ProjectionRequest: memory.ProjectionRequest{
			AgentID:    pkgroot.NAME,
			SessionKey: "tool-hint-session",
			MaxTokens:  4096,
		},
		CurrentMessage: "Capture these commitments and give me a reminder/follow-up plan with explicit timing.",
	})
	require.NoError(t, err)
	require.NotNil(t, built.Projection)

	var systemText string
	for _, seg := range built.Projection.Segments {
		if seg.Kind == memory.ProjectionSegmentSystem && seg.Source == "runtime_system" {
			systemText = seg.Text
			break
		}
	}
	require.NotEmpty(t, systemText)
	assert.Contains(t, systemText, "`memory`")
	assert.NotContains(t, systemText, "`obligation`")
}

func insertImmutableMessage(t *testing.T, al *AgentLoop, msg *memory.ImmutableMessage) {
	t.Helper()
	require.NoError(t, al.memDelegate.InsertImmutableMessage(t.Context(), msg))
}

func projectionKinds(projection *memory.ActiveContextProjection) []memory.ProjectionSegmentKind {
	if projection == nil {
		return nil
	}

	kinds := make([]memory.ProjectionSegmentKind, 0, len(projection.Segments))
	for _, seg := range projection.Segments {
		kinds = append(kinds, seg.Kind)
	}
	return kinds
}
