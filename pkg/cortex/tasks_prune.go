package cortex

import (
	"context"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// PruneStore is the minimal interface for pruning quarantined items.
// Implemented by LibSQLDelegate via sqlc-generated queries.
type PruneStore interface {
	// ListQuarantinedRecallItems returns recall items ready for permanent deletion.
	ListQuarantinedRecallItems(ctx context.Context, agentID string, cutoff time.Time, limit int) ([]*RecallItem, error)
	// ListQuarantinedArchivalChunks returns chunks ready for permanent deletion.
	ListQuarantinedArchivalChunks(ctx context.Context, cutoff time.Time, limit int) ([]*ArchivalChunk, error)
	// HardDeleteRecallItem permanently deletes a recall item.
	HardDeleteRecallItem(ctx context.Context, agentID string, id ids.UUID) error
	// HardDeleteArchivalChunks permanently deletes chunks for a recall item.
	HardDeleteArchivalChunks(ctx context.Context, recallID ids.UUID) error
	// HardDeleteChunk permanently deletes a single archival chunk by ID.
	HardDeleteChunk(ctx context.Context, id ids.UUID) error
}

// RecallItem mirrors memory.RecallItem for the Cortex package.
type RecallItem struct {
	ID           ids.UUID
	AgentID      string
	CreatedAt    time.Time
	SuppressedAt *time.Time
}

// ArchivalChunk mirrors memory.ArchivalChunk for the Cortex package.
type ArchivalChunk struct {
	ID        ids.UUID
	RecallID  ids.UUID
	CreatedAt time.Time
}

// PruneConfig configures the prune task.
type PruneConfig struct {
	QuarantinePeriod time.Duration // How long items stay in quarantine before deletion (default 30 days)
	BatchSize        int           // Max items to prune per run (default 50)
	Interval         time.Duration // How often to run (default 1 hour)
	Timeout          time.Duration // Max execution time (default 60 seconds)
}

// DefaultPruneConfig returns sensible defaults for the prune task.
func DefaultPruneConfig() PruneConfig {
	return PruneConfig{
		QuarantinePeriod: 30 * 24 * time.Hour, // 30 days
		BatchSize:        50,
		Interval:         time.Hour,
		Timeout:          60 * time.Second,
	}
}

// PruneTask permanently deletes memory items that have been soft-deleted
// and have completed their quarantine period.
type PruneTask struct {
	cfg   PruneConfig
	store PruneStore
}

// NewPruneTask creates a prune task with the given config and store.
// If store is nil, the task becomes a no-op.
func NewPruneTask(cfg PruneConfig, store PruneStore) *PruneTask {
	return &PruneTask{cfg: cfg, store: store}
}

func (t *PruneTask) Name() string            { return "prune" }
func (t *PruneTask) Interval() time.Duration { return t.cfg.Interval }
func (t *PruneTask) Timeout() time.Duration  { return t.cfg.Timeout }

func (t *PruneTask) Execute(ctx context.Context) error {
	if t.store == nil {
		logger.DebugCF("cortex", "Prune task skipped: no store configured", nil)
		return nil
	}

	cutoff := time.Now().Add(-t.cfg.QuarantinePeriod)

	// Prune recall items
	recallItems, err := t.store.ListQuarantinedRecallItems(ctx, "", cutoff, t.cfg.BatchSize)
	if err != nil {
		return err
	}

	recallPruned := 0
	for _, item := range recallItems {
		// First delete associated archival chunks
		if err := t.store.HardDeleteArchivalChunks(ctx, item.ID); err != nil {
			logger.WarnCF("cortex", "Failed to delete archival chunks for recall item",
				map[string]interface{}{
					"recall_id": item.ID.String(),
					"error":     err.Error(),
				})
			continue
		}

		// Then delete the recall item itself
		if err := t.store.HardDeleteRecallItem(ctx, item.AgentID, item.ID); err != nil {
			logger.WarnCF("cortex", "Failed to prune recall item",
				map[string]interface{}{
					"id":    item.ID.String(),
					"error": err.Error(),
				})
			continue
		}
		recallPruned++
	}

	// Prune orphaned archival chunks (chunks without recall items)
	orphanChunks, err := t.store.ListQuarantinedArchivalChunks(ctx, cutoff, t.cfg.BatchSize)
	if err != nil {
		return err
	}

	chunkPruned := 0
	for _, chunk := range orphanChunks {
		if err := t.store.HardDeleteChunk(ctx, chunk.ID); err != nil {
			logger.WarnCF("cortex", "Failed to delete orphan archival chunk",
				map[string]interface{}{
					"chunk_id": chunk.ID.String(),
					"error":    err.Error(),
				})
			continue
		}
		chunkPruned++
	}

	if recallPruned > 0 || chunkPruned > 0 {
		logger.InfoCF("cortex", "Prune task completed",
			map[string]interface{}{
				"recall_pruned": recallPruned,
				"chunks_pruned": chunkPruned,
				"quarantine":    t.cfg.QuarantinePeriod.String(),
			})
	}

	return nil
}
