package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

// StateStore persists agent run state snapshots and transition logs.
type StateStore struct {
	q *sqlc.Queries
}

func NewStateStore(q *sqlc.Queries) *StateStore {
	return &StateStore{q: q}
}

func (s *StateStore) CreateRun(ctx context.Context, conversationID ids.UUID) (sqlc.AgentRun, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentRun{}, pcerrors.New(pcerrors.CodeUnknown, "state store is not configured")
	}
	if conversationID.IsZero() {
		return sqlc.AgentRun{}, pcerrors.New(pcerrors.CodeUnknown, "conversation id is empty")
	}

	return s.q.CreateAgentRun(ctx, sqlc.CreateAgentRunParams{
		ID:             ids.New(),
		ConversationID: conversationID,
		Status:         "running",
		MetadataJson:   json.RawMessage(`{}`),
	})
}

func (s *StateStore) UpdateRunStatus(ctx context.Context, runID ids.UUID, status string, meta map[string]any) (sqlc.AgentRun, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentRun{}, pcerrors.New(pcerrors.CodeUnknown, "state store is not configured")
	}
	if runID.IsZero() {
		return sqlc.AgentRun{}, pcerrors.New(pcerrors.CodeUnknown, "run id is empty")
	}
	if status == "" {
		return sqlc.AgentRun{}, pcerrors.New(pcerrors.CodeUnknown, "status is empty")
	}

	metaJSON := json.RawMessage(`{}`)
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = b
		}
	}

	return s.q.UpdateAgentRunStatus(ctx, sqlc.UpdateAgentRunStatusParams{
		Status:       status,
		MetadataJson: metaJSON,
		ID:           runID,
	})
}

func (s *StateStore) AddRunState(ctx context.Context, runID ids.UUID, stepIndex int, state fantasy.ReActState, snapshot any) (sqlc.AgentRunState, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentRunState{}, pcerrors.New(pcerrors.CodeUnknown, "state store is not configured")
	}
	if runID.IsZero() {
		return sqlc.AgentRunState{}, pcerrors.New(pcerrors.CodeUnknown, "run id is empty")
	}
	if stepIndex < 0 {
		return sqlc.AgentRunState{}, pcerrors.New(pcerrors.CodeUnknown, "step index is negative")
	}

	snapJSON := json.RawMessage(`{}`)
	if snapshot != nil {
		if b, err := json.Marshal(snapshot); err == nil {
			snapJSON = b
		}
	}

	return s.q.AddAgentRunState(ctx, sqlc.AddAgentRunStateParams{
		ID:           ids.New(),
		RunID:        runID,
		StepIndex:    int64(stepIndex),
		State:        string(state),
		SnapshotJson: snapJSON,
	})
}

func (s *StateStore) AddTransition(ctx context.Context, runID ids.UUID, t fantasy.ReActTransition) (sqlc.AgentStateTransition, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentStateTransition{}, pcerrors.New(pcerrors.CodeUnknown, "state store is not configured")
	}
	if runID.IsZero() {
		return sqlc.AgentStateTransition{}, pcerrors.New(pcerrors.CodeUnknown, "run id is empty")
	}

	metaJSON := json.RawMessage(`{}`)
	if t.Meta != nil {
		if b, err := json.Marshal(t.Meta); err == nil {
			metaJSON = b
		}
	}

	var errPtr *string
	if t.Error != "" {
		errPtr = &t.Error
	}

	at := t.At
	if at.IsZero() {
		at = time.Now().UTC()
	}

	return s.q.AddAgentStateTransition(ctx, sqlc.AddAgentStateTransitionParams{
		ID:        ids.New(),
		RunID:     runID,
		StepIndex: int64(t.StepIndex),
		FromState: string(t.From),
		ToState:   string(t.To),
		Trigger:   string(t.Trigger),
		At:        at,
		MetaJson:  metaJSON,
		Error:     errPtr,
	})
}

// CheckpointStore persists named checkpoints for later restore.
type CheckpointStore struct {
	q *sqlc.Queries
}

func NewCheckpointStore(q *sqlc.Queries) *CheckpointStore {
	return &CheckpointStore{q: q}
}

func (s *CheckpointStore) CreateCheckpoint(ctx context.Context, conversationID ids.UUID, name string, runStateID ids.UUID, meta map[string]any) (sqlc.AgentCheckpoint, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "checkpoint store is not configured")
	}
	if conversationID.IsZero() {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "conversation id is empty")
	}
	if strings.TrimSpace(name) == "" {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "checkpoint name is empty")
	}
	if runStateID.IsZero() {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "run state id is empty")
	}

	metaJSON := json.RawMessage(`{}`)
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = b
		}
	}

	return s.q.CreateAgentCheckpoint(ctx, sqlc.CreateAgentCheckpointParams{
		ID:             ids.New(),
		ConversationID: conversationID,
		Name:           strings.TrimSpace(name),
		RunStateID:     runStateID,
		MetadataJson:   metaJSON,
	})
}

func (s *CheckpointStore) ListCheckpoints(ctx context.Context, conversationID ids.UUID) ([]sqlc.AgentCheckpoint, error) {
	if s == nil || s.q == nil {
		return nil, pcerrors.New(pcerrors.CodeUnknown, "checkpoint store is not configured")
	}
	if conversationID.IsZero() {
		return nil, pcerrors.New(pcerrors.CodeUnknown, "conversation id is empty")
	}
	return s.q.ListAgentCheckpointsByConversationID(ctx, sqlc.ListAgentCheckpointsByConversationIDParams{
		ConversationID: conversationID,
	})
}

func (s *CheckpointStore) GetCheckpoint(ctx context.Context, conversationID ids.UUID, name string) (sqlc.AgentCheckpoint, error) {
	if s == nil || s.q == nil {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "checkpoint store is not configured")
	}
	if conversationID.IsZero() {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "conversation id is empty")
	}
	if strings.TrimSpace(name) == "" {
		return sqlc.AgentCheckpoint{}, pcerrors.New(pcerrors.CodeUnknown, "checkpoint name is empty")
	}
	return s.q.GetAgentCheckpointByConversationIDAndName(ctx, sqlc.GetAgentCheckpointByConversationIDAndNameParams{
		ConversationID: conversationID,
		Name:           strings.TrimSpace(name),
	})
}
