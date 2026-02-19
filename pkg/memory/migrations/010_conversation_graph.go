package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up010ConversationGraph, down010ConversationGraph)
}

func up010ConversationGraph(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_conversation_forks (
    id BLOB PRIMARY KEY,
    parent_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    child_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    checkpoint_id BLOB NOT NULL REFERENCES agent_checkpoints(id) ON DELETE RESTRICT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(child_conversation_id)
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_conversation_forks_parent_created_at ON agent_conversation_forks(parent_conversation_id, created_at DESC)`,

		`CREATE TABLE IF NOT EXISTS agent_conversation_links (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    linked_conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'merge',
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(conversation_id, linked_conversation_id, kind)
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_conversation_links_conversation_id_created_at ON agent_conversation_links(conversation_id, created_at DESC)`,

		`CREATE TABLE IF NOT EXISTS agent_threads (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    title TEXT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_threads_conversation_id_created_at ON agent_threads(conversation_id, created_at DESC)`,

		`CREATE TABLE IF NOT EXISTS agent_thread_messages (
    id BLOB PRIMARY KEY,
    thread_id BLOB NOT NULL REFERENCES agent_threads(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_thread_messages_thread_id_created_at ON agent_thread_messages(thread_id, created_at ASC)`,

		`CREATE TABLE IF NOT EXISTS agent_mentions (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    message_id BLOB REFERENCES agent_messages(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    target_id BLOB NOT NULL,
    raw TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_mentions_conversation_id_created_at ON agent_mentions(conversation_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_mentions_kind_target_id ON agent_mentions(kind, target_id)`,

		`CREATE TABLE IF NOT EXISTS agent_message_revisions (
    id BLOB PRIMARY KEY,
    message_id BLOB NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
    editor TEXT NOT NULL,
    old_content TEXT NOT NULL,
    new_content TEXT NOT NULL,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_message_revisions_message_id_created_at ON agent_message_revisions(message_id, created_at DESC)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("010_conversation_graph up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down010ConversationGraph(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS agent_message_revisions`,
		`DROP TABLE IF EXISTS agent_mentions`,
		`DROP TABLE IF EXISTS agent_thread_messages`,
		`DROP TABLE IF EXISTS agent_threads`,
		`DROP TABLE IF EXISTS agent_conversation_links`,
		`DROP TABLE IF EXISTS agent_conversation_forks`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("010_conversation_graph down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
