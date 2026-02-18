package delegate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLibSQLDelegate_GetKV(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID   string
		key       string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "missing key returns empty string",
			agentID:   "agent-1",
			key:       "nonexistent",
			wantValue: "",
		},
		{
			name: "returns stored value",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "agent-1", "greeting", "hello"))
			},
			agentID:   "agent-1",
			key:       "greeting",
			wantValue: "hello",
		},
		{
			name: "agent isolation — different agent sees empty",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "agent-A", "secret", "mine"))
			},
			agentID:   "agent-B",
			key:       "secret",
			wantValue: "",
		},
		{
			name: "empty value is valid",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "agent-1", "blank", ""))
			},
			agentID:   "agent-1",
			key:       "blank",
			wantValue: "",
		},
		{
			name: "value with special characters",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "agent-1", "json", `{"key":"val","n":42}`))
			},
			agentID:   "agent-1",
			key:       "json",
			wantValue: `{"key":"val","n":42}`,
		},
		{
			name: "key with colons and slashes",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "agent-1", "focus:session/abc", "data"))
			},
			agentID:   "agent-1",
			key:       "focus:session/abc",
			wantValue: "data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := context.Background()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			got, err := d.GetKV(ctx, tt.agentID, tt.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, got)
		})
	}
}

func TestLibSQLDelegate_UpsertKV(t *testing.T) {
	tests := []struct {
		name    string
		ops     []kvOp
		agentID string
		key     string
		want    string
	}{
		{
			name:    "insert new key",
			ops:     []kvOp{{agent: "a1", key: "k1", val: "v1"}},
			agentID: "a1",
			key:     "k1",
			want:    "v1",
		},
		{
			name: "overwrite existing key",
			ops: []kvOp{
				{agent: "a1", key: "k1", val: "first"},
				{agent: "a1", key: "k1", val: "second"},
			},
			agentID: "a1",
			key:     "k1",
			want:    "second",
		},
		{
			name: "overwrite with empty",
			ops: []kvOp{
				{agent: "a1", key: "k1", val: "nonempty"},
				{agent: "a1", key: "k1", val: ""},
			},
			agentID: "a1",
			key:     "k1",
			want:    "",
		},
		{
			name: "same key different agents are independent",
			ops: []kvOp{
				{agent: "a1", key: "shared", val: "from-a1"},
				{agent: "a2", key: "shared", val: "from-a2"},
			},
			agentID: "a1",
			key:     "shared",
			want:    "from-a1",
		},
		{
			name: "large value roundtrip",
			ops: []kvOp{
				{agent: "a1", key: "big", val: longValue(4096)},
			},
			agentID: "a1",
			key:     "big",
			want:    longValue(4096),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := context.Background()
			for _, op := range tt.ops {
				require.NoError(t, d.UpsertKV(ctx, op.agent, op.key, op.val))
			}
			got, err := d.GetKV(ctx, tt.agentID, tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLibSQLDelegate_DeleteKV(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		key     string
		wantErr bool
	}{
		{
			name:    "delete nonexistent key is idempotent",
			agentID: "a1",
			key:     "nope",
		},
		{
			name: "delete existing key",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "a1", "doomed", "bye"))
			},
			agentID: "a1",
			key:     "doomed",
		},
		{
			name: "delete only affects target agent",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "a1", "shared", "a1-val"))
				require.NoError(t, d.UpsertKV(ctx, "a2", "shared", "a2-val"))
			},
			agentID: "a1",
			key:     "shared",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := context.Background()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			err := d.DeleteKV(ctx, tt.agentID, tt.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			got, err := d.GetKV(ctx, tt.agentID, tt.key)
			require.NoError(t, err)
			assert.Empty(t, got, "key should be gone after delete")

			if tt.name == "delete only affects target agent" {
				other, err := d.GetKV(ctx, "a2", "shared")
				require.NoError(t, err)
				assert.Equal(t, "a2-val", other, "other agent's key must survive")
			}
		})
	}
}

func TestLibSQLDelegate_ListKVByPrefix(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		prefix  string
		limit   int
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "empty store returns empty map",
			agentID: "a1",
			prefix:  "obs:",
			limit:   10,
			want:    map[string]string{},
		},
		{
			name: "matches prefix exactly",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "a1", "obs:1", "one"))
				require.NoError(t, d.UpsertKV(ctx, "a1", "obs:2", "two"))
				require.NoError(t, d.UpsertKV(ctx, "a1", "focus:x", "other"))
			},
			agentID: "a1",
			prefix:  "obs:",
			limit:   10,
			want:    map[string]string{"obs:1": "one", "obs:2": "two"},
		},
		{
			name: "respects limit",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				for i := 0; i < 5; i++ {
					require.NoError(t, d.UpsertKV(ctx, "a1", kvKey("item:", i), kvVal(i)))
				}
			},
			agentID: "a1",
			prefix:  "item:",
			limit:   3,
			want:    nil, // checked via length only
		},
		{
			name: "agent isolation in prefix scan",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "a1", "ns:k1", "a1-val"))
				require.NoError(t, d.UpsertKV(ctx, "a2", "ns:k2", "a2-val"))
			},
			agentID: "a1",
			prefix:  "ns:",
			limit:   10,
			want:    map[string]string{"ns:k1": "a1-val"},
		},
		{
			name: "prefix with no trailing separator",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertKV(ctx, "a1", "abc", "one"))
				require.NoError(t, d.UpsertKV(ctx, "a1", "abd", "two"))
				require.NoError(t, d.UpsertKV(ctx, "a1", "xyz", "three"))
			},
			agentID: "a1",
			prefix:  "ab",
			limit:   10,
			want:    map[string]string{"abc": "one", "abd": "two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := context.Background()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			got, err := d.ListKVByPrefix(ctx, tt.agentID, tt.prefix, tt.limit)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.name == "respects limit" {
				assert.LessOrEqual(t, len(got), tt.limit)
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

// --- helpers ---

type kvOp struct {
	agent, key, val string
}

func longValue(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'A' + byte(i%26)
	}
	return string(b)
}

func kvKey(prefix string, i int) string {
	return prefix + string(rune('0'+i))
}

func kvVal(i int) string {
	return string(rune('a'+i)) + "-val"
}
