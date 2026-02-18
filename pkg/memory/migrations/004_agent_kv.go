package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up004AgentKV, down004AgentKV)
}

func up004AgentKV(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS agent_kv (
    agent_id   TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, key)
)`)
	if err != nil {
		return fmt.Errorf("004_agent_kv up: %w", err)
	}
	return nil
}

func down004AgentKV(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS agent_kv`)
	if err != nil {
		return fmt.Errorf("004_agent_kv down: %w", err)
	}
	return nil
}
