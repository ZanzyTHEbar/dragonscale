package dag

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
)

const dagBackfillStatusKVKey = "migration:dag_backfill:v1"

// BackfillOptions controls DAG backfill behavior.
type BackfillOptions struct {
	PageSize    int
	MaxSessions int
	Force       bool
}

func DefaultBackfillOptions() BackfillOptions {
	return BackfillOptions{
		PageSize:    500,
		MaxSessions: 0,
		Force:       false,
	}
}

// BackfillStatus tracks one DAG backfill pass over existing sessions.
type BackfillStatus struct {
	Version          int       `json:"version"`
	SessionsScanned  int       `json:"sessions_scanned"`
	SnapshotsCreated int       `json:"snapshots_created"`
	SkippedExisting  int       `json:"skipped_existing"`
	Failures         int       `json:"failures"`
	CompletedAt      time.Time `json:"completed_at"`
	Skipped          bool      `json:"skipped,omitempty"`
}

// BackfillMissingSessionDAGs creates DAG snapshots for sessions that have
// session-message history but no persisted DAG snapshot yet.
//
// The pass is one-shot by default and records status in KV under
// dagBackfillStatusKVKey. Use opts.Force=true to run again.
func BackfillMissingSessionDAGs(
	ctx context.Context,
	delegate memory.MemoryDelegate,
	queries *memsqlc.Queries,
	agentID string,
	opts BackfillOptions,
) (*BackfillStatus, error) {
	if delegate == nil || queries == nil {
		return nil, nil
	}
	persister, ok := delegate.(DAGPersister)
	if !ok {
		return nil, nil
	}

	if opts.PageSize <= 0 {
		opts.PageSize = 500
	}

	if !opts.Force {
		if raw, err := delegate.GetKV(ctx, agentID, dagBackfillStatusKVKey); err == nil && strings.TrimSpace(raw) != "" {
			var status BackfillStatus
			if uErr := jsonv2.Unmarshal([]byte(raw), &status); uErr == nil {
				status.Skipped = true
				return &status, nil
			}
			return &BackfillStatus{Version: 1, Skipped: true}, nil
		}
	}

	sessionKeys, err := collectSessionKeysForBackfill(ctx, delegate, agentID, opts.PageSize)
	if err != nil {
		return nil, err
	}
	if opts.MaxSessions > 0 && len(sessionKeys) > opts.MaxSessions {
		sessionKeys = sessionKeys[:opts.MaxSessions]
	}

	status := &BackfillStatus{
		Version:         1,
		SessionsScanned: len(sessionKeys),
	}
	compressor := NewCompressor(DefaultCompressorConfig())

	for _, sessionKey := range sessionKeys {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}

		_, err := queries.GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
			AgentID:    agentID,
			SessionKey: sessionKey,
		})
		if err == nil {
			status.SkippedExisting++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			status.Failures++
			continue
		}

		msgs, err := loadSessionMessagesForBackfill(ctx, delegate, agentID, sessionKey, opts.PageSize)
		if err != nil {
			status.Failures++
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		d := compressor.Compress(msgs)
		if d == nil || len(d.Nodes) == 0 {
			continue
		}

		if err := persister.PersistDAG(ctx, agentID, sessionKey, &PersistSnapshot{
			FromMsgIdx: 0,
			ToMsgIdx:   len(msgs),
			MsgCount:   len(msgs),
			DAG:        d,
		}); err != nil {
			status.Failures++
			continue
		}
		status.SnapshotsCreated++
	}

	status.CompletedAt = time.Now().UTC()
	data, err := jsonv2.Marshal(status)
	if err != nil {
		return status, err
	}
	if err := delegate.UpsertKV(ctx, agentID, dagBackfillStatusKVKey, string(data)); err != nil {
		return status, err
	}
	return status, nil
}

func collectSessionKeysForBackfill(ctx context.Context, delegate memory.MemoryDelegate, agentID string, pageSize int) ([]string, error) {
	keys := make(map[string]struct{})
	offset := 0
	for {
		items, err := delegate.ListRecallItems(ctx, agentID, "", pageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if !strings.Contains(item.Tags, "session-message") {
				continue
			}
			keys[item.SessionKey] = struct{}{}
		}
		if len(items) < pageSize {
			break
		}
		offset += len(items)
	}

	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func loadSessionMessagesForBackfill(
	ctx context.Context,
	delegate memory.MemoryDelegate,
	agentID string,
	sessionKey string,
	pageSize int,
) ([]Message, error) {
	type sessionLister interface {
		ListSessionMessages(ctx context.Context, agentID, sessionKey, role string, limit int) ([]*memory.RecallItem, error)
	}

	if lister, ok := delegate.(sessionLister); ok {
		count, err := delegate.CountRecallItems(ctx, agentID, sessionKey)
		if err != nil {
			count = 0
		}
		limit := count + 32
		if limit < 32 {
			limit = 32
		}
		rows, err := lister.ListSessionMessages(ctx, agentID, sessionKey, "", limit)
		if err == nil {
			msgs := make([]Message, 0, len(rows))
			for _, row := range rows {
				msgs = append(msgs, Message{Role: row.Role, Content: row.Content})
			}
			return msgs, nil
		}
	}

	var all []*memory.RecallItem
	offset := 0
	for {
		items, err := delegate.ListRecallItems(ctx, agentID, sessionKey, pageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		all = append(all, items...)
		if len(items) < pageSize {
			break
		}
		offset += len(items)
	}

	msgs := make([]Message, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		item := all[i]
		if !strings.Contains(item.Tags, "session-message") {
			continue
		}
		msgs = append(msgs, Message{Role: item.Role, Content: item.Content})
	}
	return msgs, nil
}
