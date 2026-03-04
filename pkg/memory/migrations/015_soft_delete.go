package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up015SoftDelete, down015SoftDelete)
}

func up015SoftDelete(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		// Add suppressed_at column to recall_items for soft delete
		`ALTER TABLE recall_items ADD COLUMN suppressed_at DATETIME`,
		`CREATE INDEX IF NOT EXISTS idx_recall_suppressed ON recall_items(suppressed_at)`,

		// Add suppressed_at column to archival_chunks for soft delete
		`ALTER TABLE archival_chunks ADD COLUMN suppressed_at DATETIME`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_suppressed ON archival_chunks(suppressed_at)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("015_soft_delete up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down015SoftDelete(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP INDEX IF EXISTS idx_recall_suppressed`,
		`DROP INDEX IF EXISTS idx_chunks_suppressed`,
		// SQLite doesn't support DROP COLUMN directly; would need table recreation
		// Leaving columns as-is for safety; they are nullable and ignored by queries
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("015_soft_delete down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
