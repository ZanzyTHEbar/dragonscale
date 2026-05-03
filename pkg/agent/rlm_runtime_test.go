package agent

import (
	"context"
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

func TestAssembleContext_ReducesProjectionWithRLM(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-rlm-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Memory.DBPath = filepath.Join(tmpDir, "agent-rlm.db")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel(t, "ok"))
	mock := &mockRLMAnswerer{answer: "critical dependency: prior DAG summary says to preserve the write-path invariants"}
	al.rlmEngine = mock
	al.rlmDirectThresholdBytes = 1

	sessionKey := "rlm-session"
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
		Summary:  "DAG summary: preserve the write-path invariants and downstream dependency ordering",
		Tokens:   32,
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

	ac, err := al.assembleContext(t.Context(), processOptions{
		SessionKey:    sessionKey,
		UserMessage:   "What dependency matters?",
		EnableSummary: false,
	})
	require.NoError(t, err)
	require.NotNil(t, ac.projection)

	assert.Equal(t, 1, mock.calls)
	assert.Equal(t, "What dependency matters?", mock.query)
	assert.Contains(t, mock.context, "DAG summary: preserve the write-path invariants")
	assert.Contains(t, ac.systemPrompt, "Recursive Context Reduction")
	assert.Contains(t, ac.systemPrompt, mock.answer)
	assert.NotContains(t, projectionKinds(ac.projection), memory.ProjectionSegmentDAG)
}

type mockRLMAnswerer struct {
	answer  string
	context string
	query   string
	calls   int
}

func (m *mockRLMAnswerer) Answer(_ context.Context, _ string, query, context_ string) (string, uint32, error) {
	m.calls++
	m.query = query
	m.context = context_
	return m.answer, 17, nil
}
