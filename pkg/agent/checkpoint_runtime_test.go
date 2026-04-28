package agent

import (
	"context"
	"encoding/json"
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
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
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

func TestAgentLoop_RestartReusesConversationBinding(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-checkpoint-restart-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Memory.DBPath = filepath.Join(tmpDir, "agent-restart.db")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	al1 := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("first response"))
	sessionKey := "restart-session"
	_, err = al1.processMessage(ctx, bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "first turn",
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	firstConversationID, err := al1.lookupConversationIDForSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NoError(t, al1.sessions.Save(sessionKey))
	al1.sessions.Close()

	al2 := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), newMockLanguageModel("second response"))
	_, err = al2.processMessage(ctx, bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "second turn",
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	secondConversationID, err := al2.lookupConversationIDForSession(ctx, sessionKey)
	require.NoError(t, err)

	assert.Equal(t, firstConversationID, secondConversationID)
	conversations, err := al2.queries.ListAgentConversations(ctx, memsqlc.ListAgentConversationsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, conversations, 1)
	latestRun, err := al2.queries.GetLatestAgentRunByConversationID(ctx, memsqlc.GetLatestAgentRunByConversationIDParams{ConversationID: secondConversationID})
	require.NoError(t, err)
	assert.Equal(t, secondConversationID, latestRun.ConversationID)
	assert.Equal(t, sessionKey, al2.state.GetLastSessionKey())
	boundRaw, err := al2.memDelegate.GetKV(ctx, pkgroot.NAME, conversationBindingKey(sessionKey))
	require.NoError(t, err)
	assert.Equal(t, secondConversationID.String(), boundRaw)
	al2.Stop()
}

func TestAgentLoop_HeartbeatUsesUniqueSessionAndLastPersistedSessionContext(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "heartbeat reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	userSessionKey := "heartbeat-source-session"
	al.sessions.SetSummary(userSessionKey, "summary from persisted session")
	require.NoError(t, al.state.SetLastSessionKeyForTarget(ctx, "test", "chat1", userSessionKey))

	response1, err := al.ProcessHeartbeat(ctx, "heartbeat prompt", "test", "chat1")
	require.NoError(t, err)
	assert.Contains(t, response1, "heartbeat reply")

	response2, err := al.ProcessHeartbeat(ctx, "heartbeat prompt", "test", "chat1")
	require.NoError(t, err)
	assert.Contains(t, response2, "heartbeat reply")

	conversations, err := al.queries.ListAgentConversations(ctx, memsqlc.ListAgentConversationsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, conversations, 2)
	for _, conv := range conversations {
		require.NotNil(t, conv.Title)
		assert.Contains(t, *conv.Title, "heartbeat:")
		assert.NotEqual(t, "heartbeat", *conv.Title)
	}
	assert.Equal(t, userSessionKey, al.state.GetLastSessionKey())
}

func TestAgentLoop_HeartbeatUsesTargetScopedSessionContext(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "heartbeat reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	al.sessions.SetSummary("session-chat-a", "summary from chat A")
	al.sessions.SetSummary("session-chat-b", "summary from chat B")
	require.NoError(t, al.state.SetLastSessionKeyForTarget(ctx, "telegram", "chat-a", "session-chat-a"))
	require.NoError(t, al.state.SetLastSessionKeyForTarget(ctx, "telegram", "chat-b", "session-chat-b"))

	responseA, err := al.ProcessHeartbeat(ctx, "heartbeat prompt", "telegram", "chat-a")
	require.NoError(t, err)
	assert.Contains(t, responseA, "heartbeat reply")

	responseB, err := al.ProcessHeartbeat(ctx, "heartbeat prompt", "telegram", "chat-b")
	require.NoError(t, err)
	assert.Contains(t, responseB, "heartbeat reply")

	conversations, err := al.queries.ListAgentConversations(ctx, memsqlc.ListAgentConversationsParams{Limit: 20})
	require.NoError(t, err)
	require.Len(t, conversations, 2)
	for _, conv := range conversations {
		require.NotNil(t, conv.Title)
		assert.Contains(t, *conv.Title, "heartbeat:")
	}
	assert.Equal(t, "session-chat-b", al.state.GetLastSessionKey())
	assert.Equal(t, "session-chat-a", al.state.GetLastSessionKeyForTarget("telegram", "chat-a"))
	assert.Equal(t, "session-chat-b", al.state.GetLastSessionKeyForTarget("telegram", "chat-b"))
}

func TestAgentLoop_ProcessDirectStreamingPersistsLastSessionKey(t *testing.T) {
	t.Parallel()

	al := newCheckpointTestAgentLoop(t, "streaming reply")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	sessionKey := "streaming-session"
	response, err := al.ProcessDirectStreaming(ctx, "stream this", sessionKey, "cli", "stream-chat")
	require.NoError(t, err)
	assert.Contains(t, response, "streaming reply")
	assert.Equal(t, sessionKey, al.state.GetLastSessionKey())
}

func TestIntegration_RuntimeCheckpoint_SubagentSnapshotIncludesToolMessages(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-checkpoint-subagent-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.Model = "multi-tool-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Memory.DBPath = filepath.Join(tmpDir, "agent-checkpoint-subagent.db")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &multiToolCallingModel{})
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	runLoop := MakeUnifiedRunLoopFunc(al)
	result, err := runLoop(ctx, tools.ToolLoopConfig{Model: &multiToolCallingModel{}, Tools: al.tools, Bus: msgBus, MaxIterations: 10}, "", "Use the echo tool twice", "test", "chat1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Final response after two tools", result.Content)

	convs, err := al.queries.ListAgentConversations(ctx, memsqlc.ListAgentConversationsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, convs, 1)

	run, err := al.queries.GetLatestAgentRunByConversationID(ctx, memsqlc.GetLatestAgentRunByConversationIDParams{ConversationID: convs[0].ID})
	require.NoError(t, err)
	checkpoint := mustGetCheckpoint(t, al, convs[0].ID, checkpointNameForRun(run.ID))

	var meta map[string]any
	require.NoError(t, json.Unmarshal(checkpoint.MetadataJson, &meta))
	assert.Equal(t, float64(2), meta["tool_calls"])
	assert.Equal(t, float64(0), meta["errors"])

	runState, err := al.queries.GetAgentRunStateByID(ctx, memsqlc.GetAgentRunStateByIDParams{ID: checkpoint.RunStateID})
	require.NoError(t, err)
	snapshot, err := conversations.DecodeCheckpointSnapshot(runState.SnapshotJson)
	require.NoError(t, err)

	history := checkpointHistoryView(snapshot.Messages)
	assert.Contains(t, history, "tool:Echo: one")
	assert.Contains(t, history, "tool:Echo: two")
	assert.Contains(t, history[len(history)-1], "assistant:Final response after two tools")
}

func TestIntegration_RuntimeCheckpoint_SubagentRecoveredFinalPersistsAssistantMessage(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "agent-checkpoint-subagent-recovered-final-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Sandbox = tmpDir
	cfg.Agents.Defaults.Model = "empty-final-after-tool-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Memory.DBPath = filepath.Join(tmpDir, "agent-checkpoint-subagent-recovered-final.db")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &emptyFinalAfterToolModel{})
	al.RegisterTool(&echoTool{})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	runLoop := MakeUnifiedRunLoopFunc(al)
	result, err := runLoop(ctx, tools.ToolLoopConfig{Model: &emptyFinalAfterToolModel{}, Tools: al.tools, Bus: msgBus, MaxIterations: 10}, "", "Recover the final content from the tool result", "test", "chat1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Echo: recovered final", result.Content)

	convs, err := al.queries.ListAgentConversations(ctx, memsqlc.ListAgentConversationsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, convs, 1)

	run, err := al.queries.GetLatestAgentRunByConversationID(ctx, memsqlc.GetLatestAgentRunByConversationIDParams{ConversationID: convs[0].ID})
	require.NoError(t, err)
	checkpoint := mustGetCheckpoint(t, al, convs[0].ID, checkpointNameForRun(run.ID))
	runState, err := al.queries.GetAgentRunStateByID(ctx, memsqlc.GetAgentRunStateByIDParams{ID: checkpoint.RunStateID})
	require.NoError(t, err)
	snapshot, err := conversations.DecodeCheckpointSnapshot(runState.SnapshotJson)
	require.NoError(t, err)

	history := checkpointHistoryView(snapshot.Messages)
	require.NotEmpty(t, history)
	assert.Contains(t, history[len(history)-1], "assistant:Echo: recovered final")

	sessionHistory := checkpointHistoryView(al.sessions.GetHistory(*convs[0].Title))
	require.NotEmpty(t, sessionHistory)
	assert.Contains(t, sessionHistory[len(sessionHistory)-1], "assistant:Echo: recovered final")
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
