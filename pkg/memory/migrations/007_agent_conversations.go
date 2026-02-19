package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up007AgentConversations, down007AgentConversations)
}

func up007AgentConversations(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_conversations (
    id BLOB PRIMARY KEY,
    title TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE TABLE IF NOT EXISTS agent_messages (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_conversation_created_at ON agent_messages(conversation_id, created_at)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("007_agent_conversations up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down007AgentConversations(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS agent_messages`,
		`DROP TABLE IF EXISTS agent_conversations`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("007_agent_conversations down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
