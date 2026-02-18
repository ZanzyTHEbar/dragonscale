package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up005AgentDocuments, down005AgentDocuments)
}

func up005AgentDocuments(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_documents (
    id         BLOB PRIMARY KEY,
    agent_id   TEXT NOT NULL,
    name       TEXT NOT NULL,
    category   TEXT NOT NULL DEFAULT 'bootstrap',
    content    TEXT NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1,
    is_active  INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_id, name)
)`,
		`CREATE INDEX IF NOT EXISTS idx_docs_agent_cat ON agent_documents(agent_id, category)`,
		`CREATE INDEX IF NOT EXISTS idx_docs_name ON agent_documents(name)`,
	}

	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("005_agent_documents up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down005AgentDocuments(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS agent_documents`)
	if err != nil {
		return fmt.Errorf("005_agent_documents down: %w", err)
	}
	return nil
}
