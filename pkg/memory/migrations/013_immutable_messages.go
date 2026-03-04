package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up013ImmutableMessages, down013ImmutableMessages)
}

func up013ImmutableMessages(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS immutable_messages (
    id BLOB PRIMARY KEY,
    session_key TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_call_id TEXT NOT NULL DEFAULT '',
    tool_calls TEXT NOT NULL DEFAULT '',
    token_estimate INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_immutable_session_created ON immutable_messages(session_key, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_immutable_token_estimate ON immutable_messages(token_estimate)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("013_immutable_messages up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down013ImmutableMessages(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP INDEX IF EXISTS idx_immutable_token_estimate`,
		`DROP INDEX IF EXISTS idx_immutable_session_created`,
		`DROP TABLE IF EXISTS immutable_messages`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("013_immutable_messages down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
