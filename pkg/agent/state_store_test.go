package agent_test

import (
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/agent"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

func newTestQueries(t *testing.T) *testDB {
	t.Helper()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err, "NewLibSQLInMemory")
	require.NoError(t, d.Init(t.Context()), "Init")
	t.Cleanup(func() { _ = d.Close() })
	return &testDB{delegate: d}
}

// testDB is a small fixture that holds the delegate so tests can access
// both the high-level KV API and the raw sqlc.Queries.
type testDB struct {
	delegate *delegate.LibSQLDelegate
}

// newConversation inserts a minimal conversation row and returns its ID.
// Required because agent_runs has a FK to agent_conversations.
func newConversation(t *testing.T, q *sqlc.Queries) ids.UUID {
	t.Helper()
	id := ids.New()
	title := "test-conv"
	_, err := q.CreateAgentConversation(t.Context(), sqlc.CreateAgentConversationParams{
		ID:    id,
		Title: &title,
	})
	require.NoError(t, err, "CreateAgentConversation")
	return id
}

func TestStateStore_CreateRun(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	assert.False(t, run.ID.IsZero(), "run ID must be set")
	assert.Empty(t, cmp.Diff(sqlc.AgentRun{
		ConversationID: convID,
		Status:         "running",
	}, run, cmpopts.IgnoreFields(sqlc.AgentRun{}, "ID", "MetadataJson", "CreatedAt", "UpdatedAt")))
}

func TestStateStore_CreateRun_ZeroConversationID(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	s := agent.NewStateStore(db.delegate.Queries())
	ctx := t.Context()

	_, err := s.CreateRun(ctx, ids.UUID{})
	assert.Error(t, err, "zero conversation id should be rejected")
}

func TestStateStore_UpdateRunStatus(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	updated, err := s.UpdateRunStatus(ctx, run.ID, "completed", map[string]any{"steps": 3})
	require.NoError(t, err)

	assert.Empty(t, cmp.Diff(sqlc.AgentRun{
		ID:     run.ID,
		Status: "completed",
	}, updated, cmpopts.IgnoreFields(sqlc.AgentRun{}, "ConversationID", "MetadataJson", "CreatedAt", "UpdatedAt")))
}

func TestStateStore_UpdateRunStatus_EmptyStatus(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	_, err = s.UpdateRunStatus(ctx, run.ID, "", nil)
	assert.Error(t, err, "empty status should be rejected")
}

func TestStateStore_AddRunState(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	state, err := s.AddRunState(ctx, run.ID, 0, fantasy.ReActStateLLMCall, map[string]string{"model": "gpt-4o"})
	require.NoError(t, err)

	assert.Empty(t, cmp.Diff(sqlc.AgentRunState{
		RunID:     run.ID,
		StepIndex: 0,
		State:     string(fantasy.ReActStateLLMCall),
	}, state, cmpopts.IgnoreFields(sqlc.AgentRunState{}, "ID", "SnapshotJson", "CreatedAt", "UpdatedAt")))
}

func TestStateStore_AddRunState_NegativeStep(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	_, err = s.AddRunState(ctx, run.ID, -1, fantasy.ReActStateLLMCall, nil)
	assert.Error(t, err, "negative step index should be rejected")
}

func TestStateStore_AddTransition(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	tr := fantasy.ReActTransition{
		From:      fantasy.ReActStateInit,
		To:        fantasy.ReActStatePrepareStep,
		Trigger:   fantasy.ReActTriggerStart,
		At:        time.Now().UTC(),
		StepIndex: 0,
	}
	row, err := s.AddTransition(ctx, run.ID, tr)
	require.NoError(t, err)

	assert.Empty(t, cmp.Diff(sqlc.AgentStateTransition{
		RunID:     run.ID,
		StepIndex: 0,
		FromState: string(fantasy.ReActStateInit),
		ToState:   string(fantasy.ReActStatePrepareStep),
		Trigger:   string(fantasy.ReActTriggerStart),
	}, row, cmpopts.IgnoreFields(sqlc.AgentStateTransition{}, "ID", "At", "MetaJson", "Error", "CreatedAt", "UpdatedAt")))
}

func TestStateStore_NilStore(t *testing.T) {
	t.Parallel()
	var s *agent.StateStore
	ctx := t.Context()

	_, err := s.CreateRun(ctx, ids.New())
	assert.Error(t, err, "nil store should error")
}

// --- CheckpointStore ---

func TestCheckpointStore_CreateAndList(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	ss := agent.NewStateStore(q)
	cs := agent.NewCheckpointStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := ss.CreateRun(ctx, convID)
	require.NoError(t, err)

	runState, err := ss.AddRunState(ctx, run.ID, 0, fantasy.ReActStateDone, nil)
	require.NoError(t, err)

	cp, err := cs.CreateCheckpoint(ctx, convID, "after-step-0", runState.ID, map[string]any{"note": "test"})
	require.NoError(t, err)

	assert.False(t, cp.ID.IsZero())
	assert.Empty(t, cmp.Diff("after-step-0", cp.Name))

	cps, err := cs.ListCheckpoints(ctx, convID)
	require.NoError(t, err)
	assert.Len(t, cps, 1)
	assert.Empty(t, cmp.Diff(cp.ID, cps[0].ID))
}

func TestCheckpointStore_GetByName(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	ss := agent.NewStateStore(q)
	cs := agent.NewCheckpointStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	run, err := ss.CreateRun(ctx, convID)
	require.NoError(t, err)

	runState, err := ss.AddRunState(ctx, run.ID, 0, fantasy.ReActStateDone, nil)
	require.NoError(t, err)

	_, err = cs.CreateCheckpoint(ctx, convID, "snap-1", runState.ID, nil)
	require.NoError(t, err)

	cp, err := cs.GetCheckpoint(ctx, convID, "snap-1")
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("snap-1", cp.Name))
}

func TestCheckpointStore_EmptyName(t *testing.T) {
	t.Parallel()
	db := newTestQueries(t)
	q := db.delegate.Queries()
	cs := agent.NewCheckpointStore(q)
	ctx := t.Context()

	convID := newConversation(t, q)
	_, err := cs.CreateCheckpoint(ctx, convID, "   ", ids.New(), nil)
	assert.Error(t, err, "blank name should be rejected")
}
