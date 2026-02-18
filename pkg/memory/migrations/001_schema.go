package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up001Schema, down001Schema)
}

func up001Schema(ctx context.Context, tx *sql.Tx) error {
	dims := embeddingDimsFromContext(ctx)

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS working_context (
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, session_key)
)`,
		`CREATE TABLE IF NOT EXISTS recall_items (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'system',
    sector TEXT NOT NULL DEFAULT 'episodic',
    importance REAL NOT NULL DEFAULT 0.5,
    salience REAL NOT NULL DEFAULT 0.5,
    decay_rate REAL NOT NULL DEFAULT 0.01,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_agent_session ON recall_items(agent_id, session_key)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_sector ON recall_items(sector)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_importance ON recall_items(importance DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_created ON recall_items(created_at DESC)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS archival_chunks (
    id BLOB PRIMARY KEY,
    recall_id BLOB NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding F32_BLOB(%d),
    source TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`, dims),
		`CREATE INDEX IF NOT EXISTS idx_chunks_recall ON archival_chunks(recall_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_source ON archival_chunks(source)`,
		`CREATE TABLE IF NOT EXISTS memory_summaries (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    from_msg_idx INTEGER NOT NULL DEFAULT 0,
    to_msg_idx INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_agent_session ON memory_summaries(agent_id, session_key)`,
		`CREATE TRIGGER IF NOT EXISTS recall_cascade_delete
AFTER DELETE ON recall_items BEGIN
    DELETE FROM archival_chunks WHERE recall_id = old.id;
END`,
	}

	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("001_schema up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down001Schema(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TRIGGER IF EXISTS recall_cascade_delete`,
		`DROP TABLE IF EXISTS memory_summaries`,
		`DROP TABLE IF EXISTS archival_chunks`,
		`DROP TABLE IF EXISTS recall_items`,
		`DROP TABLE IF EXISTS working_context`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("001_schema down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
