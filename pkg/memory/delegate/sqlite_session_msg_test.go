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

func makeRecallItem(agentID, sessionKey, role, content, tags string) *memory.RecallItem {
	now := time.Now()
	return &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Role:       role,
		Sector:     memory.SectorEpisodic,
		Importance: 0.5,
		Salience:   0.5,
		DecayRate:  0.01,
		Content:    content,
		Tags:       tags,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestLibSQLDelegate_InsertSessionMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		agentID    string
		sessionKey string
		role       string
		content    string
	}{
		{
			name:       "insert user message",
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "user",
			content:    "Hello, agent!",
		},
		{
			name:       "insert assistant message",
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "assistant",
			content:    "Hello, human!",
		},
		{
			name:       "insert tool message",
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "tool",
			content:    `{"result":"ok"}`,
		},
		{
			name:       "empty content is valid",
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "system",
			content:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()

			require.NoError(t, d.InsertSessionMessage(ctx, tt.agentID, tt.sessionKey, tt.role, tt.content))

			count, err := d.CountSessionMessages(ctx, tt.agentID, tt.sessionKey)
			require.NoError(t, err)
			assert.Equal(t, int64(1), count)
		})
	}
}

func TestLibSQLDelegate_ListSessionMessages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID    string
		sessionKey string
		role       string
		limit      int
		wantLen    int
		wantRole   string
	}{
		{
			name:       "empty session returns empty",
			agentID:    "a1",
			sessionKey: "sess-empty",
			role:       "",
			limit:      50,
			wantLen:    0,
		},
		{
			name: "list all roles (role=empty string)",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "msg1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "assistant", "msg2"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "tool", "msg3"))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "",
			limit:      50,
			wantLen:    3,
		},
		{
			name: "filter by role=user",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "u1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "assistant", "a1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "u2"))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "user",
			limit:      50,
			wantLen:    2,
			wantRole:   "user",
		},
		{
			name: "session isolation",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-A", "user", "msgA"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-B", "user", "msgB"))
			},
			agentID:    "a1",
			sessionKey: "sess-A",
			role:       "",
			limit:      50,
			wantLen:    1,
		},
		{
			name: "agent isolation",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "from-a1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a2", "sess-1", "user", "from-a2"))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "",
			limit:      50,
			wantLen:    1,
		},
		{
			name: "respects limit",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				for i := 0; i < 10; i++ {
					require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", kvVal(i)))
				}
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "",
			limit:      5,
			wantLen:    5,
		},
		{
			name: "ordered by created_at ASC",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "first"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "assistant", "second"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "third"))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			role:       "",
			limit:      50,
			wantLen:    3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			msgs, err := d.ListSessionMessages(ctx, tt.agentID, tt.sessionKey, tt.role, tt.limit)
			require.NoError(t, err)
			assert.Len(t, msgs, tt.wantLen)

			if tt.wantRole != "" {
				for _, m := range msgs {
					assert.Equal(t, tt.wantRole, m.Role)
				}
			}

			if tt.name == "ordered by created_at ASC" && len(msgs) == 3 {
				assert.Equal(t, "first", msgs[0].Content)
				assert.Equal(t, "second", msgs[1].Content)
				assert.Equal(t, "third", msgs[2].Content)
			}
		})
	}
}

func TestLibSQLDelegate_CountSessionMessages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID    string
		sessionKey string
		want       int64
	}{
		{
			name:       "empty session returns zero",
			agentID:    "a1",
			sessionKey: "sess-empty",
			want:       0,
		},
		{
			name: "counts session messages only",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "m1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "assistant", "m2"))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			want:       2,
		},
		{
			name: "excludes non-session recall items",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-1", "user", "session-msg"))
				// Insert a generic recall item via the standard method
				item := makeRecallItem("a1", "sess-1", "user", "generic-item", "other-tag")
				require.NoError(t, d.InsertRecallItem(ctx, item))
			},
			agentID:    "a1",
			sessionKey: "sess-1",
			want:       1,
		},
		{
			name: "session isolation in count",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-A", "user", "m1"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-A", "user", "m2"))
				require.NoError(t, d.InsertSessionMessage(ctx, "a1", "sess-B", "user", "m3"))
			},
			agentID:    "a1",
			sessionKey: "sess-A",
			want:       2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			count, err := d.CountSessionMessages(ctx, tt.agentID, tt.sessionKey)
			require.NoError(t, err)
			assert.Equal(t, tt.want, count)
		})
	}
}
