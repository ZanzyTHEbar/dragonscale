package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up006AgentAuditLog, down006AgentAuditLog)
}

func up006AgentAuditLog(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_audit_log (
    id          BLOB PRIMARY KEY,
    agent_id    TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    input       TEXT,
    output      TEXT,
    duration_ms INTEGER,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_agent_time ON agent_audit_log(agent_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_action ON agent_audit_log(action)`,
	}

	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("006_agent_audit_log up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down006AgentAuditLog(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS agent_audit_log`)
	if err != nil {
		return fmt.Errorf("006_agent_audit_log down: %w", err)
	}
	return nil
}
