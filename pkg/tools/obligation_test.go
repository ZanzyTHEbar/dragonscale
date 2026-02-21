package tools

import (
	"context"
	"testing"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
)

func TestObligationTool_CreateAndList(t *testing.T) {
	ctx := context.Background()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))
	defer del.Close()

	tool := NewObligationTool(del, "test-agent")
	dueAt := time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339)
	create := tool.Execute(ctx, map[string]interface{}{
		"action": "create",
		"title":  "send weekly update",
		"due_at": dueAt,
	})
	require.NotNil(t, create)
	require.False(t, create.IsError, create.ForLLM)

	var rec ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(create.ForLLM), &rec))
	require.NotEmpty(t, rec.ID)
	assert.Equal(t, ObligationStateScheduled, rec.State)

	list := tool.Execute(ctx, map[string]interface{}{"action": "list"})
	require.NotNil(t, list)
	require.False(t, list.IsError, list.ForLLM)

	var payload struct {
		Count int `json:"count"`
	}
	require.NoError(t, jsonv2.Unmarshal([]byte(list.ForLLM), &payload))
	assert.GreaterOrEqual(t, payload.Count, 1)
}

func TestObligationTool_StateMachineAndEvidence(t *testing.T) {
	ctx := context.Background()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))
	defer del.Close()

	tool := NewObligationTool(del, "test-agent")
	create := tool.Execute(ctx, map[string]interface{}{
		"action": "create",
		"title":  "follow up with candidate",
	})
	require.False(t, create.IsError, create.ForLLM)

	var rec ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(create.ForLLM), &rec))

	invalid := tool.Execute(ctx, map[string]interface{}{
		"action":        "update_state",
		"obligation_id": rec.ID,
		"state":         "verified",
	})
	require.True(t, invalid.IsError)
	assert.Contains(t, invalid.ForLLM, "invalid obligation transition")

	toDue := tool.Execute(ctx, map[string]interface{}{
		"action":        "update_state",
		"obligation_id": rec.ID,
		"state":         "due",
	})
	require.False(t, toDue.IsError, toDue.ForLLM)

	toExecuted := tool.Execute(ctx, map[string]interface{}{
		"action":        "update_state",
		"obligation_id": rec.ID,
		"state":         "executed",
	})
	require.False(t, toExecuted.IsError, toExecuted.ForLLM)

	missingEvidence := tool.Execute(ctx, map[string]interface{}{
		"action":        "update_state",
		"obligation_id": rec.ID,
		"state":         "verified",
	})
	require.True(t, missingEvidence.IsError)
	assert.Contains(t, missingEvidence.ForLLM, "requires evidence")

	withEvidence := tool.Execute(ctx, map[string]interface{}{
		"action":        "add_evidence",
		"obligation_id": rec.ID,
		"evidence":      "sent confirmation email",
		"source":        "email",
	})
	require.False(t, withEvidence.IsError, withEvidence.ForLLM)

	toVerified := tool.Execute(ctx, map[string]interface{}{
		"action":        "update_state",
		"obligation_id": rec.ID,
		"state":         "verified",
	})
	require.False(t, toVerified.IsError, toVerified.ForLLM)

	var verified ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(toVerified.ForLLM), &verified))
	assert.Equal(t, ObligationStateVerified, verified.State)
	assert.NotZero(t, verified.VerifiedAt)
	require.Len(t, verified.Evidence, 1)
}

func TestObligationTool_CollectDueObligations_TransitionsScheduledToDue(t *testing.T) {
	ctx := context.Background()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))
	defer del.Close()

	tool := NewObligationTool(del, "test-agent")
	create := tool.Execute(ctx, map[string]interface{}{
		"action": "create",
		"title":  "send reminder",
		"due_at": time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339),
	})
	require.False(t, create.IsError, create.ForLLM)

	var created ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(create.ForLLM), &created))
	require.Equal(t, ObligationStateScheduled, created.State)

	due, err := tool.CollectDueObligations(ctx, time.Now().UTC(), "heartbeat")
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, created.ID, due[0].ID)
	assert.Equal(t, ObligationStateDue, due[0].State)
	require.NotEmpty(t, due[0].Evidence)
	assert.Equal(t, "heartbeat", due[0].Evidence[0].Source)

	get := tool.Execute(ctx, map[string]interface{}{
		"action":        "get",
		"obligation_id": created.ID,
	})
	require.False(t, get.IsError, get.ForLLM)

	var persisted ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(get.ForLLM), &persisted))
	assert.Equal(t, ObligationStateDue, persisted.State)
	require.Len(t, persisted.Evidence, 1)
}

func TestObligationTool_CollectDueObligations_DoesNotDuplicateDueEvidence(t *testing.T) {
	ctx := context.Background()
	del, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, del.Init(ctx))
	defer del.Close()

	tool := NewObligationTool(del, "test-agent")
	create := tool.Execute(ctx, map[string]interface{}{
		"action": "create",
		"title":  "follow up now",
		"due_at": time.Now().UTC().Add(-1 * time.Minute).Format(time.RFC3339),
	})
	require.False(t, create.IsError, create.ForLLM)

	var created ObligationRecord
	require.NoError(t, jsonv2.Unmarshal([]byte(create.ForLLM), &created))

	first, err := tool.CollectDueObligations(ctx, time.Now().UTC(), "heartbeat")
	require.NoError(t, err)
	require.Len(t, first, 1)

	second, err := tool.CollectDueObligations(ctx, time.Now().UTC().Add(1*time.Minute), "heartbeat")
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Len(t, second[0].Evidence, 1, "due transition evidence should be appended only once")
	assert.Equal(t, created.ID, second[0].ID)
}
