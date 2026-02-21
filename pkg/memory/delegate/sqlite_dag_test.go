package delegate

import (
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLibSQLDelegate_PersistDAG(t *testing.T) {
	t.Parallel()
	d := newTestDelegate(t)
	ctx := t.Context()

	compressor := dag.NewCompressor(dag.DefaultCompressorConfig())
	msgs := make([]dag.Message, 16)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = dag.Message{Role: role, Content: "msg " + string(rune('A'+i%26))}
	}
	dagOut := compressor.Compress(msgs)
	require.NotEmpty(t, dagOut.Nodes)

	snap := &dag.PersistSnapshot{
		FromMsgIdx: 0,
		ToMsgIdx:   16,
		MsgCount:   16,
		DAG:        dagOut,
	}
	require.NoError(t, d.PersistDAG(ctx, "agent1", "session1", snap))

	row, err := d.Queries().GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
		AgentID:    "agent1",
		SessionKey: "session1",
	})
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("agent1", row.AgentID))
	assert.Empty(t, cmp.Diff("session1", row.SessionKey))
	assert.Empty(t, cmp.Diff(int64(16), row.MsgCount))

	nodes, err := d.Queries().ListDAGNodesBySnapshotID(ctx, memsqlc.ListDAGNodesBySnapshotIDParams{
		SnapshotID: row.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, nodes)
}
