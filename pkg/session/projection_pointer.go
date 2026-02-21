package session

import (
	"context"
	"fmt"
	"time"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

const (
	projectionKVPrefix      = "session:projection:"
	projectionBackfillKVKey = "migration:session_projection_backfill:v1"
)

// ProjectionBackfillStatus tracks execution of the projection-pointer migration
// backfill pass for continuity-safe restore.
type ProjectionBackfillStatus struct {
	Version         int       `json:"version"`
	SessionsScanned int       `json:"sessions_scanned"`
	PointersUpdated int       `json:"pointers_updated"`
	CompletedAt     time.Time `json:"completed_at"`
}

// ProjectionPointer holds metadata for deterministic session resume and integrity validation.
// Persisted via agent_kv, keyed by session. Used to detect stream continuity breaks on restore.
type ProjectionPointer struct {
	FirstMessageID ids.UUID  `json:"first_message_id"`
	LastMessageID  ids.UUID  `json:"last_message_id"`
	FirstCreatedAt time.Time `json:"first_created_at"`
	LastCreatedAt  time.Time `json:"last_created_at"`
	Count          int       `json:"count"`
}

// projectionPointerKey returns the KV key for a session's projection pointer.
func projectionPointerKey(sessionKey string) string {
	return projectionKVPrefix + sessionKey
}

func projectionBackfillStatusKey() string {
	return projectionBackfillKVKey
}

// projectFromItems computes a projection pointer from a chronologically ordered stream of recall items.
func projectFromItems(items []*memory.RecallItem) *ProjectionPointer {
	if len(items) == 0 {
		return nil
	}
	return &ProjectionPointer{
		FirstMessageID: items[0].ID,
		LastMessageID:  items[len(items)-1].ID,
		FirstCreatedAt: items[0].CreatedAt,
		LastCreatedAt:  items[len(items)-1].CreatedAt,
		Count:          len(items),
	}
}

// loadProjectionPointer reads the stored pointer from delegate KV.
func (sm *SessionManager) loadProjectionPointer(ctx context.Context, sessionKey string) (*ProjectionPointer, error) {
	if sm.delegate == nil {
		return nil, nil
	}
	raw, err := sm.delegate.GetKV(ctx, sm.agentID, projectionPointerKey(sessionKey))
	if err != nil || raw == "" {
		return nil, err
	}
	var p ProjectionPointer
	if err := jsonv2.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// persistProjectionPointer writes the pointer to delegate KV.
func (sm *SessionManager) persistProjectionPointer(ctx context.Context, sessionKey string, p *ProjectionPointer) error {
	if sm.delegate == nil || p == nil {
		return nil
	}
	data, err := jsonv2.Marshal(p)
	if err != nil {
		return err
	}
	return sm.delegate.UpsertKV(ctx, sm.agentID, projectionPointerKey(sessionKey), string(data))
}

func projectionPointerEqual(a *ProjectionPointer, b *ProjectionPointer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Count == b.Count &&
		a.FirstMessageID == b.FirstMessageID &&
		a.LastMessageID == b.LastMessageID &&
		a.FirstCreatedAt.Equal(b.FirstCreatedAt) &&
		a.LastCreatedAt.Equal(b.LastCreatedAt)
}

func (sm *SessionManager) persistProjectionBackfillStatus(ctx context.Context, status ProjectionBackfillStatus) error {
	if sm.delegate == nil {
		return nil
	}
	data, err := jsonv2.Marshal(status)
	if err != nil {
		return err
	}
	return sm.delegate.UpsertKV(ctx, sm.agentID, projectionBackfillStatusKey(), string(data))
}

// validateAndLogRestore compares prior pointer to newly restored stream; logs diagnostics on mismatch.
// Restore proceeds regardless; this is diagnostic only.
// Validates count and message IDs (deterministic); skips timestamp comparison since DB may truncate.
func (sm *SessionManager) validateAndLogRestore(sessionKey string, prior *ProjectionPointer, restored *ProjectionPointer) {
	if prior == nil || restored == nil {
		return
	}
	mismatches := []string{}
	if prior.Count != restored.Count {
		mismatches = append(mismatches, fmt.Sprintf("count %d -> %d", prior.Count, restored.Count))
	}
	if prior.FirstMessageID != restored.FirstMessageID {
		mismatches = append(mismatches, fmt.Sprintf("first_id %s -> %s", prior.FirstMessageID, restored.FirstMessageID))
	}
	if prior.LastMessageID != restored.LastMessageID {
		mismatches = append(mismatches, fmt.Sprintf("last_id %s -> %s", prior.LastMessageID, restored.LastMessageID))
	}
	if len(mismatches) > 0 {
		logger.WarnCF("session", "Restore integrity mismatch",
			map[string]interface{}{
				"session":    sessionKey,
				"mismatches": mismatches,
			})
	}
}

// advancePointer updates a pointer when a new message is appended.
// If prior is nil, the new message is the first.
func advancePointer(prior *ProjectionPointer, newID ids.UUID, newCreatedAt time.Time) *ProjectionPointer {
	if prior == nil {
		return &ProjectionPointer{
			FirstMessageID: newID,
			LastMessageID:  newID,
			FirstCreatedAt: newCreatedAt,
			LastCreatedAt:  newCreatedAt,
			Count:          1,
		}
	}
	return &ProjectionPointer{
		FirstMessageID: prior.FirstMessageID,
		LastMessageID:  newID,
		FirstCreatedAt: prior.FirstCreatedAt,
		LastCreatedAt:  newCreatedAt,
		Count:          prior.Count + 1,
	}
}
