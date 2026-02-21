package dag_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

func TestBackfillMissingSessionDAGs_CreatesSnapshotsAndPersistsStatus(t *testing.T) {
	ctx := context.Background()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(ctx))
	defer d.Close()

	agentID := "agent-backfill"
	sessionKey := "legacy-session-1"
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		require.NoError(t, d.InsertRecallItem(ctx, &memory.RecallItem{
			ID:         ids.New(),
			AgentID:    agentID,
			SessionKey: sessionKey,
			Role:       role,
			Sector:     memory.SectorEpisodic,
			Importance: 0.5,
			Salience:   0.5,
			DecayRate:  0.01,
			Content:    fmt.Sprintf("legacy message %d", i),
			Tags:       "session-message",
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}))
	}

	status, err := dag.BackfillMissingSessionDAGs(ctx, d, d.Queries(), agentID, dag.DefaultBackfillOptions())
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, 1, status.SnapshotsCreated)
	assert.Equal(t, 0, status.Failures)

	row, err := d.Queries().GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(12), row.MsgCount)

	kv, err := d.ListKVByPrefix(ctx, agentID, "migration:dag_backfill", 10)
	require.NoError(t, err)
	require.NotEmpty(t, kv)

	var stored dag.BackfillStatus
	for _, raw := range kv {
		require.NoError(t, jsonv2.Unmarshal([]byte(raw), &stored))
		break
	}
	assert.Equal(t, status.SnapshotsCreated, stored.SnapshotsCreated)
	assert.False(t, stored.CompletedAt.IsZero())

	status2, err := dag.BackfillMissingSessionDAGs(ctx, d, d.Queries(), agentID, dag.DefaultBackfillOptions())
	require.NoError(t, err)
	require.NotNil(t, status2)
	assert.True(t, status2.Skipped)
}
