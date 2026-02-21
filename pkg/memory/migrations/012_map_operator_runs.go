package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up012MapOperatorRuns, down012MapOperatorRuns)
}

func up012MapOperatorRuns(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS map_runs (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL,
    operator_kind TEXT NOT NULL,
    idempotency_key TEXT,
    status TEXT NOT NULL DEFAULT 'queued',
    total_items INTEGER NOT NULL DEFAULT 0,
    queued_items INTEGER NOT NULL DEFAULT 0,
    running_items INTEGER NOT NULL DEFAULT 0,
    succeeded_items INTEGER NOT NULL DEFAULT 0,
    failed_items INTEGER NOT NULL DEFAULT 0,
    spec_fb BLOB NOT NULL,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    completed_at DATETIME
)`,
		`CREATE INDEX IF NOT EXISTS idx_map_runs_agent_session_created_at ON map_runs(agent_id, session_key, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_map_runs_status_updated_at ON map_runs(status, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_map_runs_dedupe ON map_runs(agent_id, session_key, operator_kind, idempotency_key) WHERE idempotency_key IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS map_items (
    id BLOB PRIMARY KEY,
    run_id BLOB NOT NULL REFERENCES map_runs(id) ON DELETE CASCADE,
    item_index INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    input_fb BLOB NOT NULL,
    output_fb BLOB,
    input_hash TEXT,
    output_hash TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    completed_at DATETIME,
    UNIQUE(run_id, item_index)
)`,
		`CREATE INDEX IF NOT EXISTS idx_map_items_run_id_status_item_index ON map_items(run_id, status, item_index)`,
		`CREATE INDEX IF NOT EXISTS idx_map_items_run_id_item_index ON map_items(run_id, item_index)`,
	}

	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("012_map_operator_runs up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down012MapOperatorRuns(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS map_items`,
		`DROP TABLE IF EXISTS map_runs`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("012_map_operator_runs down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
