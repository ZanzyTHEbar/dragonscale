package delegate

import (
	"context"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAuditEntry(agentID, sessionKey, action, target string) *memory.AuditEntry {
	return &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Action:     action,
		Target:     target,
		Input:      `{"arg":"val"}`,
		Output:     `{"result":"ok"}`,
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
			assert.Equal(t, 1, count)
		})
	}
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
				for _, e := range entries {
					assert.Equal(t, tt.wantAction, e.Action)
				}
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
	assert.Equal(t, 2, count)

	// Prune entries created before "now + 1 minute" (should remove all)
	require.NoError(t, d.PruneOldAuditEntries(ctx, "a1", time.Now().Add(time.Minute)))

	count, err = d.CountAuditEntries(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
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
			assert.Equal(t, tt.want, count)
		})
	}
}
