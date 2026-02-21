package tools

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDAGTools(t *testing.T) (DAGToolDeps, *delegate.LibSQLDelegate) {
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))

	// Insert session messages so dag_expand/dag_grep have data
	agentID, sessionKey := "test-agent", "test-session"
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		content := "Message number " + strconv.Itoa(i)
		require.NoError(t, d.InsertSessionMessage(t.Context(), agentID, sessionKey, role, content))
	}

	// Persist a DAG snapshot
	compressor := dag.NewCompressor(dag.DefaultCompressorConfig())
	msgs := make([]dag.Message, 16)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = dag.Message{Role: role, Content: "Message " + string(rune('A'+i%26))}
	}
	dagOut := compressor.Compress(msgs)
	require.NoError(t, d.PersistDAG(t.Context(), agentID, sessionKey, &dag.PersistSnapshot{
		FromMsgIdx: 0,
		ToMsgIdx:   16,
		MsgCount:   16,
		DAG:        dagOut,
	}))

	// Session messages in delegate are in InsertSessionMessage order; ListSessionMessages returns chron order.
	// The DAG was built from different content - our test inserts have "Message number X".
	// For dag_expand we need the Lister to return messages that match the DAG's indices.
	// Actually the DAG's start_idx/end_idx refer to the compressible slice at compression time.
	// The session has 20 messages. The DAG we persisted covers indices 0-16 of "compressible".
	// When we ListSessionMessages we get 20 messages. The DAG node chunk-1 might span 0-8.
	// So we'd get messages[0:8] from the list. That should work.

	sessionKeyFn := func() string { return sessionKey }
	return DAGToolDeps{
		Queries:   d.Queries(),
		Lister:    d,
		Delegate:  d,
		AgentID:   agentID,
		SessionFn: sessionKeyFn,
	}, d
}

func TestDagDescribeTool(t *testing.T) {
	t.Parallel()
	deps, del := setupDAGTools(t)
	defer del.Close()

	tool := NewDagDescribeTool(deps)
	ctx := t.Context()

	// Describe an existing node (chunk-1 from default config with 16 msgs)
	res := tool.Execute(ctx, map[string]interface{}{"node_id": "chunk-1", "session_key": "test-session"})
	assert.False(t, res.IsError)
	assert.Contains(t, res.ForLLM, "chunk-1")
	assert.Contains(t, res.ForLLM, "chunk")
}

func TestDagDescribeTool_NoSnapshot(t *testing.T) {
	t.Parallel()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	defer d.Close()
	require.NoError(t, d.Init(t.Context()))

	tool := NewDagDescribeTool(DAGToolDeps{
		Queries:   d.Queries(),
		Lister:    d,
		AgentID:   "x",
		SessionFn: func() string { return "nonexistent" },
	})
	res := tool.Execute(t.Context(), map[string]interface{}{"node_id": "chunk-1"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "no DAG snapshot")
}

func TestDagGrepTool(t *testing.T) {
	t.Parallel()
	deps, del := setupDAGTools(t)
	defer del.Close()

	tool := NewDagGrepTool(deps)
	ctx := t.Context()

	res := tool.Execute(ctx, map[string]interface{}{"query": "Message", "session_key": "test-session"})
	assert.False(t, res.IsError)
	// Should find matches in session history
	assert.Contains(t, res.ForLLM, "[")
}

func TestDagGrepTool_ScopedByNode(t *testing.T) {
	t.Parallel()
	deps, del := setupDAGTools(t)
	defer del.Close()

	tool := NewDagGrepTool(deps)
	ctx := t.Context()

	res := tool.Execute(ctx, map[string]interface{}{
		"query":       "number",
		"session_key": "test-session",
		"node_id":     "chunk-1",
	})
	// chunk-1 spans messages 0-8; our test messages have "Message number X"
	assert.False(t, res.IsError)
}

func TestDagExpandTool(t *testing.T) {
	t.Parallel()
	deps, del := setupDAGTools(t)
	defer del.Close()

	tool := NewDagExpandTool(deps)
	ctx := t.Context()

	// Expand chunk-1; needs Lister to return messages
	res := tool.Execute(ctx, map[string]interface{}{"node_id": "chunk-1", "session_key": "test-session"})
	assert.False(t, res.IsError)
	// Should contain message content from the session
	assert.Contains(t, res.ForLLM, "chunk-1")
	assert.Contains(t, res.ForLLM, "user")
	// Our InsertSessionMessage content was "Message number X"
	assert.Contains(t, res.ForLLM, "Message")
}

func TestDagExpandTool_NoReader(t *testing.T) {
	t.Parallel()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	defer d.Close()
	require.NoError(t, d.Init(t.Context()))

	// Persist minimal DAG
	compressor := dag.NewCompressor(dag.DefaultCompressorConfig())
	msgs := []dag.Message{{Role: "user", Content: "x"}}
	dagOut := compressor.Compress(msgs)
	require.NoError(t, d.PersistDAG(t.Context(), "a", "s", &dag.PersistSnapshot{
		FromMsgIdx: 0, ToMsgIdx: 1, MsgCount: 1, DAG: dagOut,
	}))

	tool := NewDagExpandTool(DAGToolDeps{
		Queries:   nil,
		Lister:    nil,
		AgentID:   "a",
		SessionFn: func() string { return "s" },
	})
	res := tool.Execute(t.Context(), map[string]interface{}{"node_id": "chunk-1", "session_key": "s"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "dag query store is not configured")
}

func TestDagExpandTool_RecoveryReference(t *testing.T) {
	t.Parallel()
	deps, del := setupDAGTools(t)
	defer del.Close()

	ctx := t.Context()
	record := DAGRecoveryRecord{
		NodeID:        DAGRecoveryNodePrefix + "test-recovery",
		SessionKey:    "test-session",
		OriginalIndex: 3,
		Role:          "user",
		Content:       "very large omitted message body",
		TokenEstimate: 12345,
		Reason:        "oversized_message_omitted_from_summary",
		CreatedAt:     time.Now().UTC(),
	}
	data, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, del.UpsertKV(ctx, deps.AgentID, DAGRecoveryKVKey(record.SessionKey, record.NodeID), string(data)))

	tool := NewDagExpandTool(deps)
	res := tool.Execute(ctx, map[string]interface{}{"node_id": record.NodeID, "session_key": record.SessionKey})
	assert.False(t, res.IsError)
	assert.Contains(t, res.ForLLM, "recovery reference")
	assert.Contains(t, res.ForLLM, record.Content)
}
