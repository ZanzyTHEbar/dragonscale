package delegate

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCronKVBackend_Roundtrip(t *testing.T) {
	d := newTestDelegate(t)
	ctx := context.Background()
	agentID := "picoclaw"
	kvKey := "cron:store"

	type cronStore struct {
		Version int `json:"version"`
		Jobs    []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"jobs"`
	}

	store := cronStore{
		Version: 1,
		Jobs: []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}{
			{ID: "job-1", Name: "daily report", Enabled: true},
			{ID: "job-2", Name: "weekly backup", Enabled: false},
		},
	}

	data, err := jsonv2.Marshal(store)
	require.NoError(t, err)

	require.NoError(t, d.UpsertKV(ctx, agentID, kvKey, string(data)))

	raw, err := d.GetKV(ctx, agentID, kvKey)
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var loaded cronStore
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &loaded))

	assert.Equal(t, 1, loaded.Version)
	assert.Len(t, loaded.Jobs, 2)
	assert.Equal(t, "daily report", loaded.Jobs[0].Name)
	assert.True(t, loaded.Jobs[0].Enabled)
	assert.Equal(t, "weekly backup", loaded.Jobs[1].Name)
	assert.False(t, loaded.Jobs[1].Enabled)
}

func TestCronKVBackend_UpdatePreservesShape(t *testing.T) {
	d := newTestDelegate(t)
	ctx := context.Background()
	agentID := "picoclaw"
	kvKey := "cron:store"

	v1 := `{"version":1,"jobs":[{"id":"j1","name":"test","enabled":true}]}`
	require.NoError(t, d.UpsertKV(ctx, agentID, kvKey, v1))

	v2 := `{"version":1,"jobs":[{"id":"j1","name":"test","enabled":true},{"id":"j2","name":"new","enabled":true}]}`
	require.NoError(t, d.UpsertKV(ctx, agentID, kvKey, v2))

	raw, err := d.GetKV(ctx, agentID, kvKey)
	require.NoError(t, err)
	assert.Equal(t, v2, raw)
}

func TestCronKVBackend_PrefixScan(t *testing.T) {
	d := newTestDelegate(t)
	ctx := context.Background()
	agentID := "picoclaw"

	require.NoError(t, d.UpsertKV(ctx, agentID, "cron:store", "{}"))
	require.NoError(t, d.UpsertKV(ctx, agentID, "cron:lock", "held"))
	require.NoError(t, d.UpsertKV(ctx, agentID, "focus:sess1", "{}"))

	result, err := d.ListKVByPrefix(ctx, agentID, "cron:", 10)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "cron:store")
	assert.Contains(t, result, "cron:lock")
}

func TestEndToEnd_SessionAndAuditFlow(t *testing.T) {
	d := newTestDelegate(t)
	ctx := context.Background()
	agentID := "a1"
	sessionKey := "sess-integration"

	// 1. Insert session messages
	require.NoError(t, d.InsertSessionMessage(ctx, agentID, sessionKey, "user", "What is the weather?"))
	require.NoError(t, d.InsertSessionMessage(ctx, agentID, sessionKey, "assistant", "Let me check..."))
	require.NoError(t, d.InsertSessionMessage(ctx, agentID, sessionKey, "tool", `{"temp":72,"unit":"F"}`))
	require.NoError(t, d.InsertSessionMessage(ctx, agentID, sessionKey, "assistant", "It's 72F."))

	// 2. Verify session message count
	count, err := d.CountSessionMessages(ctx, agentID, sessionKey)
	require.NoError(t, err)
	assert.Equal(t, int64(4), count)

	// 3. Verify message ordering (ASC)
	msgs, err := d.ListSessionMessages(ctx, agentID, sessionKey, "", 50)
	require.NoError(t, err)
	require.Len(t, msgs, 4)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "What is the weather?", msgs[0].Content)
	assert.Equal(t, "assistant", msgs[3].Role)

	// 4. Insert audit entries for the tool call
	auditEntry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Action:     "tool_call",
		Target:     "weather_api",
		Input:      `{"location":"here"}`,
		Output:     `{"temp":72}`,
		DurationMS: 150,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, auditEntry))

	// 5. Verify audit log by session
	auditEntries, err := d.ListAuditEntriesBySession(ctx, agentID, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, auditEntries, 1)
	assert.Equal(t, "tool_call", auditEntries[0].Action)
	assert.Equal(t, "weather_api", auditEntries[0].Target)

	// 6. Store KV state (e.g. focus checkpoint)
	require.NoError(t, d.UpsertKV(ctx, agentID, "focus:"+sessionKey, `{"topic":"weather query","checkpoint_index":2}`))

	kvVal, err := d.GetKV(ctx, agentID, "focus:"+sessionKey)
	require.NoError(t, err)
	assert.Contains(t, kvVal, "weather query")

	// 7. Store a document
	doc := &memory.AgentDocument{
		ID:       ids.New(),
		AgentID:  agentID,
		Name:     "AGENT.md",
		Category: "core",
		Content:  "# Agent Identity\nI am picoclaw.",
	}
	require.NoError(t, d.UpsertDocument(ctx, doc))

	loadedDoc, err := d.GetDocument(ctx, agentID, "AGENT.md")
	require.NoError(t, err)
	require.NotNil(t, loadedDoc)
	assert.Equal(t, "# Agent Identity\nI am picoclaw.", loadedDoc.Content)

	// 8. Verify cross-table isolation: different session sees nothing
	otherMsgs, err := d.ListSessionMessages(ctx, agentID, "sess-other", "", 50)
	require.NoError(t, err)
	assert.Empty(t, otherMsgs)

	otherAudit, err := d.ListAuditEntriesBySession(ctx, agentID, "sess-other", 10)
	require.NoError(t, err)
	assert.Empty(t, otherAudit)
}

func TestEndToEnd_WorkingContextAndRecallRoundtrip(t *testing.T) {
	d := newTestDelegate(t)
	ctx := context.Background()
	agentID := "a1"
	sessionKey := "sess-wc"

	// 1. Upsert working context
	require.NoError(t, d.UpsertWorkingContext(ctx, agentID, sessionKey, "Initial system prompt state"))

	wc, err := d.GetWorkingContext(ctx, agentID, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, wc)
	assert.Equal(t, "Initial system prompt state", wc.Content)

	// 2. Insert recall items
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Role:       "user",
		Sector:     memory.SectorEpisodic,
		Importance: 0.8,
		Salience:   0.7,
		DecayRate:  0.01,
		Content:    "Important user preference",
		Tags:       "preference",
	}
	require.NoError(t, d.InsertRecallItem(ctx, item))

	// 3. Count recall items
	recallCount, err := d.CountRecallItems(ctx, agentID, sessionKey)
	require.NoError(t, err)
	assert.Equal(t, 1, recallCount)

	// 4. Update working context
	require.NoError(t, d.UpsertWorkingContext(ctx, agentID, sessionKey, "Updated with preference awareness"))

	wc, err = d.GetWorkingContext(ctx, agentID, sessionKey)
	require.NoError(t, err)
	assert.Equal(t, "Updated with preference awareness", wc.Content)

	// 5. Insert a summary
	summary := &memory.MemorySummary{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Content:    "User discussed preferences. Key info captured.",
		FromMsgIdx: 0,
		ToMsgIdx:   5,
	}
	require.NoError(t, d.InsertSummary(ctx, summary))

	summaries, err := d.ListSummaries(ctx, agentID, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, "User discussed preferences. Key info captured.", summaries[0].Content)
}
