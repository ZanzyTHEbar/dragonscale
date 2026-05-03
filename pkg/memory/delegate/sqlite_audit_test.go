package delegate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAuditEntry(agentID, sessionKey, action, target string) *memory.AuditEntry {
	toolCallID := ""
	if strings.HasPrefix(action, "tool") {
		toolCallID = "call-" + target
	}
	return &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Action:     action,
		Target:     target,
		ToolCallID: toolCallID,
		Input:      `{"arg":"val"}`,
		Output:     `{"result":"ok"}`,
		Success:    true,
		DurationMS: 42,
	}
}

func TestLibSQLDelegate_InsertAuditEntry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		entry *memory.AuditEntry
	}{
		{
			name:  "insert tool_call entry",
			entry: makeAuditEntry("a1", "sess-1", "tool_call", "exec"),
		},
		{
			name:  "insert memory_write entry",
			entry: makeAuditEntry("a1", "sess-1", "memory_write", "working_context"),
		},
		{
			name: "insert entry with empty optional fields",
			entry: &memory.AuditEntry{
				ID:         ids.New(),
				AgentID:    "a1",
				SessionKey: "sess-1",
				Action:     "state_change",
				Target:     "",
				Input:      "",
				Output:     "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			require.NoError(t, d.InsertAuditEntry(ctx, tt.entry))

			count, err := d.CountAuditEntries(ctx, tt.entry.AgentID)
			require.NoError(t, err)
			testcmp.AssertEqual(t, 1, count)
		})
	}
}

func TestLibSQLDelegate_ListAuditEntries_PreservesOutcomeMetadata(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	entry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: "sess-1",
		Action:     "tool_error",
		Target:     "exec",
		ToolCallID: "call-exec",
		Input:      `{"command":"rm -rf /tmp/test"}`,
		Output:     "permission denied",
		Success:    false,
		ErrorMsg:   "permission denied",
		DurationMS: 9,
	}

	require.NoError(t, d.InsertAuditEntry(ctx, entry))

	entries, err := d.ListAuditEntries(ctx, "a1", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	testcmp.AssertEqual(t, "call-exec", entries[0].ToolCallID)
	assert.False(t, entries[0].Success)
	testcmp.AssertEqual(t, "permission denied", entries[0].ErrorMsg)
}

func TestLibSQLDelegate_GetRecentAuditEntries_HandlesMixedLegacyAndExplicitRows(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	now := time.Now().UTC()

	_, err := d.db.ExecContext(ctx, `
		INSERT INTO agent_audit_log (
			id, agent_id, session_key, action, target, input, output, duration_ms, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ids.New(), "a1", "sess-legacy", "tool_call", "legacy_read", `{"path":"legacy.txt"}`, `{"status":"ok"}`, 4, now)
	require.NoError(t, err)

	explicit := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: "sess-explicit",
		Action:     "tool_call",
		Target:     "explicit_write",
		ToolCallID: "call-explicit-write",
		Input:      `{"path":"new.txt"}`,
		Output:     "write denied",
		Success:    false,
		ErrorMsg:   "write denied",
		DurationMS: 7,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, explicit))

	entries, err := d.GetRecentAuditEntries(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byTool := make(map[string]AuditEntry, len(entries))
	for _, entry := range entries {
		byTool[entry.ToolName] = entry
	}

	legacy, ok := byTool["legacy_read"]
	require.True(t, ok, "legacy row missing")
	assert.True(t, legacy.Success)
	testcmp.AssertEqual(t, "", legacy.ErrorMsg)

	explicitEntry, ok := byTool["explicit_write"]
	require.True(t, ok, "explicit row missing")
	assert.False(t, explicitEntry.Success)
	testcmp.AssertEqual(t, "write denied", explicitEntry.ErrorMsg)
	testcmp.AssertEqual(t, "call-explicit-write", explicitEntry.ToolCallID)
	testcmp.AssertEqual(t, `{"path":"new.txt"}`, explicitEntry.ToolInput)
}

func TestLibSQLDelegate_GetRecentAuditEntries_PrefersSecureBusRowsOverLegacyToolRows(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	now := time.Now().UTC()
	toolCallID := "call-echo"
	sessionKey := "sess-securebus"

	legacy := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: sessionKey,
		Action:     "tool_success",
		Target:     "echo",
		ToolCallID: toolCallID,
		Input:      `{"text":"hello"}`,
		Output:     "Echo: hello",
		Success:    true,
		CreatedAt:  now,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, legacy))

	securebusRow := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: sessionKey,
		Action:     "securebus_tool_exec",
		Target:     "echo",
		ToolCallID: toolCallID,
		Output:     `{"command_type":"tool_exec","tool_name":"echo"}`,
		Success:    true,
		CreatedAt:  now.Add(time.Millisecond),
	}
	require.NoError(t, d.InsertAuditEntry(ctx, securebusRow))

	entries, err := d.GetRecentAuditEntries(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	testcmp.AssertEqual(t, "echo", entries[0].ToolName)
	testcmp.AssertEqual(t, toolCallID, entries[0].ToolCallID)
	testcmp.AssertEqual(t, sessionKey, entries[0].SessionID)
	testcmp.AssertEqual(t, `{"text":"hello"}`, entries[0].ToolInput)
	assert.True(t, entries[0].Success)
}

func TestLibSQLDelegate_GetRecentAuditEntries_PrefersSecureBusRowsButBackfillsLegacyErrorMetadata(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	now := time.Now().UTC()
	toolCallID := "call-fail"
	sessionKey := "sess-securebus-error"

	legacy := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: sessionKey,
		Action:     "tool_error",
		Target:     "echo",
		ToolCallID: toolCallID,
		Input:      `{"text":"boom"}`,
		Output:     "tool execution denied",
		Success:    false,
		ErrorMsg:   "tool execution denied",
		CreatedAt:  now,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, legacy))

	securebusRow := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: sessionKey,
		Action:     "securebus_tool_exec_error",
		Target:     "echo",
		ToolCallID: toolCallID,
		Output:     `{"command_type":"tool_exec","tool_name":"echo","is_error":true}`,
		Success:    false,
		CreatedAt:  now.Add(time.Millisecond),
	}
	require.NoError(t, d.InsertAuditEntry(ctx, securebusRow))

	entries, err := d.GetRecentAuditEntries(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	testcmp.AssertEqual(t, "echo", entries[0].ToolName)
	testcmp.AssertEqual(t, `{"text":"boom"}`, entries[0].ToolInput)
	assert.False(t, entries[0].Success)
	testcmp.AssertEqual(t, "tool execution denied", entries[0].ErrorMsg)
}

func TestLibSQLDelegate_GetRecentAuditEntries_PreservesLegacyRowsWithoutSecureBusTwin(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	now := time.Now().UTC()

	legacy := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "a1",
		SessionKey: "sess-legacy-only",
		Action:     "tool_error",
		Target:     "write_file",
		ToolCallID: "call-write",
		Input:      `{"path":"x"}`,
		Output:     "permission denied",
		Success:    false,
		ErrorMsg:   "permission denied",
		CreatedAt:  now,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, legacy))

	entries, err := d.GetRecentAuditEntries(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	testcmp.AssertEqual(t, "write_file", entries[0].ToolName)
	testcmp.AssertEqual(t, `{"path":"x"}`, entries[0].ToolInput)
	assert.False(t, entries[0].Success)
	testcmp.AssertEqual(t, "permission denied", entries[0].ErrorMsg)
}

func TestLibSQLDelegate_GetRecentAuditEntries_DoesNotDedupAcrossAgents(t *testing.T) {
	t.Parallel()

	d := newTestDelegate(t)
	ctx := t.Context()
	now := time.Now().UTC()
	toolCallID := "call-shared"
	sessionKey := "sess-shared"

	legacyAgentA := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "agent-a",
		SessionKey: sessionKey,
		Action:     "tool_success",
		Target:     "echo",
		ToolCallID: toolCallID,
		Input:      `{"text":"from-a"}`,
		Output:     "Echo: from-a",
		Success:    true,
		CreatedAt:  now,
	}
	require.NoError(t, d.InsertAuditEntry(ctx, legacyAgentA))

	securebusAgentB := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    "agent-b",
		SessionKey: sessionKey,
		Action:     "securebus_tool_exec",
		Target:     "echo",
		ToolCallID: toolCallID,
		Output:     `{"command_type":"tool_exec","tool_name":"echo"}`,
		Success:    true,
		CreatedAt:  now.Add(time.Millisecond),
	}
	require.NoError(t, d.InsertAuditEntry(ctx, securebusAgentB))

	entries, err := d.GetRecentAuditEntries(ctx, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byAgent := make(map[string]AuditEntry, len(entries))
	for _, entry := range entries {
		byAgent[entry.AgentID] = entry
	}
	require.Contains(t, byAgent, "agent-a")
	require.Contains(t, byAgent, "agent-b")
	testcmp.AssertEqual(t, `{"text":"from-a"}`, byAgent["agent-a"].ToolInput)
	testcmp.AssertEqual(t, "", byAgent["agent-b"].ToolInput)
	testcmp.AssertEqual(t, toolCallID, byAgent["agent-a"].ToolCallID)
	testcmp.AssertEqual(t, toolCallID, byAgent["agent-b"].ToolCallID)
}

func TestLibSQLDelegate_ListAuditEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		limit   int
		wantLen int
	}{
		{
			name:    "empty returns empty",
			agentID: "a1",
			limit:   10,
			wantLen: 0,
		},
		{
			name: "returns all for agent",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "exec")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "memory_write", "kv")))
			},
			agentID: "a1",
			limit:   10,
			wantLen: 2,
		},
		{
			name: "agent isolation",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "t1")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a2", "s1", "tool_call", "t2")))
			},
			agentID: "a1",
			limit:   10,
			wantLen: 1,
		},
		{
			name: "respects limit",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				for i := 0; i < 10; i++ {
					require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", kvKey("t", i))))
				}
			},
			agentID: "a1",
			limit:   3,
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			entries, err := d.ListAuditEntries(ctx, tt.agentID, tt.limit)
			require.NoError(t, err)
			assert.Len(t, entries, tt.wantLen)
		})
	}
}

func TestLibSQLDelegate_ListAuditEntriesByAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID    string
		action     string
		limit      int
		wantLen    int
		wantAction string
	}{
		{
			name:    "no matches returns empty",
			agentID: "a1",
			action:  "tool_call",
			limit:   10,
			wantLen: 0,
		},
		{
			name: "filters by action",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "exec")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "memory_write", "kv")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "read")))
			},
			agentID:    "a1",
			action:     "tool_call",
			limit:      10,
			wantLen:    2,
			wantAction: "tool_call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			entries, err := d.ListAuditEntriesByAction(ctx, tt.agentID, tt.action, tt.limit)
			require.NoError(t, err)
			assert.Len(t, entries, tt.wantLen)
			if tt.wantAction != "" {
				actions := make([]string, len(entries))
				for i, entry := range entries {
					actions[i] = entry.Action
				}
				wantActions := make([]string, len(entries))
				for i := range wantActions {
					wantActions[i] = tt.wantAction
				}
				testcmp.AssertEqual(t, wantActions, actions)
			}
		})
	}
}

func TestLibSQLDelegate_ListAuditEntriesBySession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID    string
		sessionKey string
		limit      int
		wantLen    int
	}{
		{
			name:       "empty returns empty",
			agentID:    "a1",
			sessionKey: "sess-1",
			limit:      10,
			wantLen:    0,
		},
		{
			name: "filters by session",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "sess-A", "tool_call", "t1")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "sess-A", "tool_call", "t2")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "sess-B", "tool_call", "t3")))
			},
			agentID:    "a1",
			sessionKey: "sess-A",
			limit:      10,
			wantLen:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			entries, err := d.ListAuditEntriesBySession(ctx, tt.agentID, tt.sessionKey, tt.limit)
			require.NoError(t, err)
			assert.Len(t, entries, tt.wantLen)
		})
	}
}

func TestLibSQLDelegate_PruneOldAuditEntries(t *testing.T) {
	t.Parallel()
	d := newTestDelegate(t)
	ctx := t.Context()

	require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "t1")))
	require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "t2")))

	count, err := d.CountAuditEntries(ctx, "a1")
	require.NoError(t, err)
	testcmp.AssertEqual(t, 2, count)

	// Prune entries created before "now + 1 minute" (should remove all)
	require.NoError(t, d.PruneOldAuditEntries(ctx, "a1", time.Now().Add(time.Minute)))

	count, err = d.CountAuditEntries(ctx, "a1")
	require.NoError(t, err)
	testcmp.AssertEqual(t, 0, count)
}

func TestLibSQLDelegate_CountAuditEntriesByAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		action  string
		want    int
	}{
		{
			name:    "no entries returns zero",
			agentID: "a1",
			action:  "tool_call",
			want:    0,
		},
		{
			name: "counts only matching action",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "t1")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "memory_write", "kv")))
				require.NoError(t, d.InsertAuditEntry(ctx, makeAuditEntry("a1", "s1", "tool_call", "t2")))
			},
			agentID: "a1",
			action:  "tool_call",
			want:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			count, err := d.CountAuditEntriesByAction(ctx, tt.agentID, tt.action)
			require.NoError(t, err)
			testcmp.AssertEqual(t, tt.want, count)
		})
	}
}
