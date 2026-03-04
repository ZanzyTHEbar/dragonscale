package cortex

import (
	"context"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// BackfillStore is the minimal interface for embedding backfill.
// Implemented by LibSQLDelegate via hand-written SQL.
type BackfillStore interface {
	CountArchivalChunksWithoutEmbedding(ctx context.Context) (int, error)
	BackfillArchivalEmbeddings(ctx context.Context, batchSize int, embedFn func(ctx context.Context, text string) ([]float32, error)) (int, error)
}

// BackfillConfig configures the embedding backfill task.
type BackfillConfig struct {
	BatchSize int           // max chunks per run (default 10)
	Interval  time.Duration // how often to run (default 2 minutes)
	Timeout   time.Duration // max execution time (default 60 seconds)
}

// DefaultBackfillConfig returns sensible defaults for embedding backfill.
func DefaultBackfillConfig() BackfillConfig {
	return BackfillConfig{
		BatchSize: 10,
		Interval:  2 * time.Minute,
		Timeout:   60 * time.Second,
	}
}

// BackfillTask processes archival chunks missing embeddings.
type BackfillTask struct {
	cfg   BackfillConfig
	store BackfillStore
	embed func(ctx context.Context, text string) ([]float32, error)
}

// NewBackfillTask creates a backfill task. If store or embed is nil, the task is a no-op.
// Validates that embedFn is not nil and logs a warning if store is nil.
func NewBackfillTask(cfg BackfillConfig, store BackfillStore, embed func(ctx context.Context, text string) ([]float32, error)) *BackfillTask {
	if embed == nil {
		logger.WarnCF("cortex", "Backfill task created with nil embed function", nil)
	}
	if store == nil {
		logger.WarnCF("cortex", "Backfill task created with nil store", nil)
	}
	return &BackfillTask{cfg: cfg, store: store, embed: embed}
}

func (t *BackfillTask) Name() string            { return "embedding_backfill" }
func (t *BackfillTask) Interval() time.Duration { return t.cfg.Interval }
func (t *BackfillTask) Timeout() time.Duration  { return t.cfg.Timeout }

func (t *BackfillTask) Execute(ctx context.Context) error {
	if t.store == nil || t.embed == nil {
		logger.DebugCF("cortex", "Backfill task skipped: store or embedder not configured", nil)
		return nil
	}

	processed, err := t.store.BackfillArchivalEmbeddings(ctx, t.cfg.BatchSize, t.embed)
	if err != nil {
		return err
	}

	if processed > 0 {
		logger.InfoCF("cortex", "Backfill task completed",
			map[string]interface{}{"processed": processed})
	}
	return nil
}
