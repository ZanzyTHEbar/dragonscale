package agent_test

import (
	"context"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory/delegate"
	"github.com/sipeed/picoclaw/pkg/memory/sqlc"
)

func newTestQueries(t *testing.T) *testDB {
	t.Helper()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err, "NewLibSQLInMemory")
	require.NoError(t, d.Init(context.Background()), "Init")
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
	_, err := q.CreateAgentConversation(context.Background(), sqlc.CreateAgentConversationParams{
		ID:    id,
		Title: &title,
	})
	require.NoError(t, err, "CreateAgentConversation")
	return id
}

func TestStateStore_CreateRun(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	assert.False(t, run.ID.IsZero(), "run ID must be set")
	assert.Equal(t, convID, run.ConversationID)
	assert.Equal(t, "running", run.Status)
}

func TestStateStore_CreateRun_ZeroConversationID(t *testing.T) {
	db := newTestQueries(t)
	s := agent.NewStateStore(db.delegate.Queries())
	ctx := context.Background()

	_, err := s.CreateRun(ctx, ids.UUID{})
	assert.Error(t, err, "zero conversation id should be rejected")
}

func TestStateStore_UpdateRunStatus(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	updated, err := s.UpdateRunStatus(ctx, run.ID, "completed", map[string]any{"steps": 3})
	require.NoError(t, err)

	assert.Equal(t, run.ID, updated.ID)
	assert.Equal(t, "completed", updated.Status)
}

func TestStateStore_UpdateRunStatus_EmptyStatus(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	_, err = s.UpdateRunStatus(ctx, run.ID, "", nil)
	assert.Error(t, err, "empty status should be rejected")
}

func TestStateStore_AddRunState(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	state, err := s.AddRunState(ctx, run.ID, 0, fantasy.ReActStateLLMCall, map[string]string{"model": "gpt-4o"})
	require.NoError(t, err)

	assert.False(t, state.ID.IsZero())
	assert.Equal(t, run.ID, state.RunID)
	assert.Equal(t, int64(0), state.StepIndex)
	assert.Equal(t, string(fantasy.ReActStateLLMCall), state.State)
}

func TestStateStore_AddRunState_NegativeStep(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := s.CreateRun(ctx, convID)
	require.NoError(t, err)

	_, err = s.AddRunState(ctx, run.ID, -1, fantasy.ReActStateLLMCall, nil)
	assert.Error(t, err, "negative step index should be rejected")
}

func TestStateStore_AddTransition(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	s := agent.NewStateStore(q)
	ctx := context.Background()

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

	assert.False(t, row.ID.IsZero())
	assert.Equal(t, string(fantasy.ReActStateInit), row.FromState)
	assert.Equal(t, string(fantasy.ReActStatePrepareStep), row.ToState)
	assert.Equal(t, string(fantasy.ReActTriggerStart), row.Trigger)
}

func TestStateStore_NilStore(t *testing.T) {
	var s *agent.StateStore
	ctx := context.Background()

	_, err := s.CreateRun(ctx, ids.New())
	assert.Error(t, err, "nil store should error")
}

// --- CheckpointStore ---

func TestCheckpointStore_CreateAndList(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	ss := agent.NewStateStore(q)
	cs := agent.NewCheckpointStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := ss.CreateRun(ctx, convID)
	require.NoError(t, err)

	runState, err := ss.AddRunState(ctx, run.ID, 0, fantasy.ReActStateDone, nil)
	require.NoError(t, err)

	cp, err := cs.CreateCheckpoint(ctx, convID, "after-step-0", runState.ID, map[string]any{"note": "test"})
	require.NoError(t, err)

	assert.False(t, cp.ID.IsZero())
	assert.Equal(t, "after-step-0", cp.Name)

	cps, err := cs.ListCheckpoints(ctx, convID)
	require.NoError(t, err)
	assert.Len(t, cps, 1)
	assert.Equal(t, cp.ID, cps[0].ID)
}

func TestCheckpointStore_GetByName(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	ss := agent.NewStateStore(q)
	cs := agent.NewCheckpointStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	run, err := ss.CreateRun(ctx, convID)
	require.NoError(t, err)

	runState, err := ss.AddRunState(ctx, run.ID, 0, fantasy.ReActStateDone, nil)
	require.NoError(t, err)

	_, err = cs.CreateCheckpoint(ctx, convID, "snap-1", runState.ID, nil)
	require.NoError(t, err)

	cp, err := cs.GetCheckpoint(ctx, convID, "snap-1")
	require.NoError(t, err)
	assert.Equal(t, "snap-1", cp.Name)
}

func TestCheckpointStore_EmptyName(t *testing.T) {
	db := newTestQueries(t)
	q := db.delegate.Queries()
	cs := agent.NewCheckpointStore(q)
	ctx := context.Background()

	convID := newConversation(t, q)
	_, err := cs.CreateCheckpoint(ctx, convID, "   ", ids.New(), nil)
	assert.Error(t, err, "blank name should be rejected")
}
