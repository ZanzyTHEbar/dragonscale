package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up009Jobs, down009Jobs)
}

func up009Jobs(_ context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
    id BLOB PRIMARY KEY,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    run_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    locked_at DATETIME,
    locked_by TEXT,
    payload_json JSON NOT NULL DEFAULT '{}',
    dedupe_key TEXT,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    completed_at DATETIME
)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_run_at ON jobs(status, run_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_kind_dedupe ON jobs(kind, dedupe_key) WHERE dedupe_key IS NOT NULL`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(context.Background(), s); err != nil {
			return fmt.Errorf("009_jobs up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down009Jobs(_ context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS jobs`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(context.Background(), s); err != nil {
			return fmt.Errorf("009_jobs down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
