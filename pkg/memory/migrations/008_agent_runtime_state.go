package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up008AgentRuntimeState, down008AgentRuntimeState)
}

func up008AgentRuntimeState(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_runs (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'running',
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runs_conversation_created_at ON agent_runs(conversation_id, created_at)`,

		`CREATE TABLE IF NOT EXISTS agent_run_states (
    id BLOB PRIMARY KEY,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    state TEXT NOT NULL,
    snapshot_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(run_id, step_index)
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_run_states_run_step ON agent_run_states(run_id, step_index)`,

		`CREATE TABLE IF NOT EXISTS agent_state_transitions (
    id BLOB PRIMARY KEY,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    trigger TEXT NOT NULL,
    at DATETIME NOT NULL,
    meta_json JSON NOT NULL DEFAULT '{}',
    error TEXT,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_state_transitions_run_step_at ON agent_state_transitions(run_id, step_index, at)`,

		`CREATE TABLE IF NOT EXISTS agent_checkpoints (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    run_state_id BLOB NOT NULL REFERENCES agent_run_states(id) ON DELETE RESTRICT,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(conversation_id, name)
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_checkpoints_conversation_created_at ON agent_checkpoints(conversation_id, created_at)`,

		`CREATE TABLE IF NOT EXISTS agent_tool_results (
    id BLOB PRIMARY KEY,
    conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    full_key TEXT NOT NULL,
    preview TEXT,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(run_id, tool_call_id)
)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tool_results_conversation_created_at ON agent_tool_results(conversation_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tool_results_run_step ON agent_tool_results(run_id, step_index)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tool_results_tool_name ON agent_tool_results(tool_name)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("008_agent_runtime_state up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down008AgentRuntimeState(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS agent_tool_results`,
		`DROP TABLE IF EXISTS agent_checkpoints`,
		`DROP TABLE IF EXISTS agent_state_transitions`,
		`DROP TABLE IF EXISTS agent_run_states`,
		`DROP TABLE IF EXISTS agent_runs`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("008_agent_runtime_state down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
