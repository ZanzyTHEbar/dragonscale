package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up016RLSchema, down016RLSchema)
}

func up016RLSchema(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		// Create task_baselines table for RL statistics per agent
		`CREATE TABLE IF NOT EXISTS task_baselines (
			agent_id TEXT PRIMARY KEY,
			count INTEGER DEFAULT 0,
			mean_tokens INTEGER DEFAULT 0,
			mean_errors REAL DEFAULT 0,
			mean_user_corrections REAL DEFAULT 0,
			m2_tokens REAL DEFAULT 0,
			m2_errors REAL DEFAULT 0,
			m2_user_corrections REAL DEFAULT 0,
			updated_at DATETIME
		)`,
		// Create task_completions table for recording completed agent runs
		`CREATE TABLE IF NOT EXISTS task_completions (
			id BLOB PRIMARY KEY,
			agent_id TEXT NOT NULL,
			conversation_id BLOB NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
			run_id BLOB NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
			description TEXT NOT NULL DEFAULT '',
			tokens_used INTEGER DEFAULT 0,
			tool_calls INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			user_corrections INTEGER DEFAULT 0,
			completed BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_completions_agent_created ON task_completions(agent_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_task_completions_run ON task_completions(run_id)`,
		// Create task_retrievals table for RL credit assignment tracking
		`CREATE TABLE IF NOT EXISTS task_retrievals (
			id BLOB PRIMARY KEY,
			task_id BLOB NOT NULL REFERENCES task_completions(id) ON DELETE CASCADE,
			memory_id BLOB NOT NULL REFERENCES recall_items(id) ON DELETE CASCADE,
			similarity REAL NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			UNIQUE(task_id, memory_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_retrievals_task ON task_retrievals(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_retrievals_memory ON task_retrievals(memory_id)`,
		// Add RL-related columns to recall_items
		`ALTER TABLE recall_items ADD COLUMN rl_weight REAL DEFAULT 1.0`,
		`ALTER TABLE recall_items ADD COLUMN rl_credit REAL`,
		`ALTER TABLE recall_items ADD COLUMN self_report_score INTEGER`,
		`ALTER TABLE recall_items ADD COLUMN task_retrieval_count INTEGER DEFAULT 0`,
		// Create index for RL weight lookups
		`CREATE INDEX IF NOT EXISTS idx_recall_rl_weight ON recall_items(rl_weight)`,
		// Create index for task retrieval tracking
		`CREATE INDEX IF NOT EXISTS idx_recall_task_retrieval ON recall_items(task_retrieval_count)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("016_rl_schema up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down016RLSchema(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		// Drop indexes first
		`DROP INDEX IF EXISTS idx_task_retrievals_memory`,
		`DROP INDEX IF EXISTS idx_task_retrievals_task`,
		`DROP INDEX IF EXISTS idx_task_completions_run`,
		`DROP INDEX IF EXISTS idx_task_completions_agent_created`,
		`DROP INDEX IF EXISTS idx_recall_task_retrieval`,
		`DROP INDEX IF EXISTS idx_recall_rl_weight`,
		// Drop tables in reverse order of creation (respect foreign keys)
		`DROP TABLE IF EXISTS task_retrievals`,
		`DROP TABLE IF EXISTS task_completions`,
		`DROP TABLE IF EXISTS task_baselines`,
		// Note: SQLite doesn't support DROP COLUMN directly
		// The columns (rl_weight, rl_credit, self_report_score, task_retrieval_count)
		// are left in place with defaults to avoid breaking existing data.
		// They will be ignored by queries that don't reference them.
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("016_rl_schema down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
