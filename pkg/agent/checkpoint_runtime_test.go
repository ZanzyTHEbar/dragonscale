package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkgroot "github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/agent/conversations"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_RuntimeCheckpoint_PersistsSnapshotAndCheckpoint(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "checkpoint reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	sessionKey := "checkpoint-session"
	response, err := al.processMessage(ctx, bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "hello checkpoint runtime",
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	assert.Contains(t, response, "checkpoint reply")
	require.NoError(t, al.sessions.Save(sessionKey))

	run, checkpoint, snapshot := loadCheckpointFixture(t, al, sessionKey)
	assert.Equal(t, "completed", run.Status)
	assert.Equal(t, checkpointTurnCompletedEvent, snapshot.Event)
	assert.NotEmpty(t, snapshot.Messages)
	assert.Equal(t, "user", snapshot.Messages[0].Role)
	assert.Equal(t, "hello checkpoint runtime", snapshot.Messages[0].Content)
	assert.Equal(t, "assistant", snapshot.Messages[len(snapshot.Messages)-1].Role)
	assert.Contains(t, snapshot.Messages[len(snapshot.Messages)-1].Content, "checkpoint reply")
	assert.Equal(t, checkpointNameForRun(run.ID), checkpoint.Name)
}

func TestAgentLoop_RestoreSessionFromCheckpoint_ReplacesPersistedHistory(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "restored checkpoint reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	sessionKey := "restore-session"
	_, err := al.processMessage(ctx, bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "restore me",
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	require.NoError(t, al.sessions.Save(sessionKey))

	run, checkpoint, snapshot := loadCheckpointFixture(t, al, sessionKey)
	require.False(t, run.ID.IsZero())

	al.sessions.AddMessage(sessionKey, "user", "mutated user")
	al.sessions.AddMessage(sessionKey, "assistant", "mutated assistant")
	require.NoError(t, al.sessions.Save(sessionKey))

	err = al.RestoreSessionFromCheckpoint(ctx, sessionKey, checkpoint.Name)
	require.NoError(t, err)

	expected := conversations.HydrationMessages(snapshot.Messages, conversations.MaxCheckpointHydrationMessages)
	assert.Equal(t, checkpointHistoryView(expected), checkpointHistoryView(al.sessions.GetHistory(sessionKey)))

	lister, ok := al.memDelegate.(sessionMessageLister)
	require.True(t, ok)
	rows, err := lister.ListSessionMessages(ctx, pkgroot.NAME, sessionKey, "", 64)
	require.NoError(t, err)
	assert.Equal(t, checkpointHistoryView(expected), recallHistoryView(rows))
}

func TestAgentLoop_ForkSessionFromCheckpoint_CreatesHydratedChildSession(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "fork checkpoint reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	sessionKey := "fork-source-session"
	_, err := al.processMessage(ctx, bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "fork me",
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	require.NoError(t, al.sessions.Save(sessionKey))

	_, checkpoint, snapshot := loadCheckpointFixture(t, al, sessionKey)
	forkSessionKey := "fork-child-session"
	childConversationID, err := al.ForkSessionFromCheckpoint(ctx, sessionKey, checkpoint.Name, forkSessionKey)
	require.NoError(t, err)
	require.False(t, childConversationID.IsZero())

	expected := conversations.HydrationMessages(snapshot.Messages, conversations.MaxCheckpointHydrationMessages)
	assert.Equal(t, checkpointHistoryView(expected), checkpointHistoryView(al.sessions.GetHistory(forkSessionKey)))

	seeded, err := al.queries.ListAgentMessagesByConversationID(ctx, memsqlc.ListAgentMessagesByConversationIDParams{
		ConversationID: childConversationID,
	})
	require.NoError(t, err)
	assert.Equal(t, checkpointHistoryView(expected), agentMessageHistoryView(seeded))
}

func newCheckpointTestAgentLoop(t *testing.T, response string) *AgentLoop {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent-checkpoint-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Memory.DBPath = filepath.Join(tmpDir, "agent-checkpoint.db")

	return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel(response))
}

func loadCheckpointFixture(t *testing.T, al *AgentLoop, sessionKey string) (memsqlc.AgentRun, memsqlc.AgentCheckpoint, conversations.CheckpointSnapshot) {
	t.Helper()

	conversationID, err := al.lookupConversationIDForSession(t.Context(), sessionKey)
	require.NoError(t, err)

	run, err := al.queries.GetLatestAgentRunByConversationID(t.Context(), memsqlc.GetLatestAgentRunByConversationIDParams{
		ConversationID: conversationID,
	})
	require.NoError(t, err)

	checkpoint := mustGetCheckpoint(t, al, conversationID, checkpointNameForRun(run.ID))
	runState, err := al.queries.GetAgentRunStateByID(t.Context(), memsqlc.GetAgentRunStateByIDParams{ID: checkpoint.RunStateID})
	require.NoError(t, err)
	snapshot, err := conversations.DecodeCheckpointSnapshot(runState.SnapshotJson)
	require.NoError(t, err)
	return run, checkpoint, snapshot
}

func mustGetCheckpoint(t *testing.T, al *AgentLoop, conversationID ids.UUID, checkpointName string) memsqlc.AgentCheckpoint {
	t.Helper()
	checkpoint, err := NewCheckpointStore(al.queries).GetCheckpoint(t.Context(), conversationID, checkpointName)
	require.NoError(t, err)
	return checkpoint
}

func checkpointHistoryView(history []messages.Message) []string {
	out := make([]string, 0, len(history))
	for _, msg := range history {
		out = append(out, msg.Role+":"+msg.Content)
	}
	return out
}

func recallHistoryView(rows []*memory.RecallItem) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Role+":"+row.Content)
	}
	return out
}

func agentMessageHistoryView(rows []memsqlc.AgentMessage) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Role+":"+row.Content)
	}
	return out
}
