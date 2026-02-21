package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up011DAGTables, down011DAGTables)
}

func up011DAGTables(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS dag_snapshots (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    from_msg_idx INTEGER NOT NULL DEFAULT 0,
    to_msg_idx INTEGER NOT NULL DEFAULT 0,
    msg_count INTEGER NOT NULL DEFAULT 0,
    roots_json TEXT NOT NULL DEFAULT '[]',
    content_hash TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_snapshots_agent_session ON dag_snapshots(agent_id, session_key)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_snapshots_created_at ON dag_snapshots(created_at DESC)`,

		`CREATE TABLE IF NOT EXISTS dag_nodes (
    id BLOB PRIMARY KEY,
    snapshot_id BLOB NOT NULL REFERENCES dag_snapshots(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    level INTEGER NOT NULL DEFAULT 1,
    summary TEXT NOT NULL DEFAULT '',
    tokens INTEGER NOT NULL DEFAULT 0,
    start_idx INTEGER NOT NULL DEFAULT 0,
    end_idx INTEGER NOT NULL DEFAULT 0,
    span INTEGER NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL DEFAULT '',
    metrics_json JSON NOT NULL DEFAULT '{}',
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_nodes_snapshot ON dag_nodes(snapshot_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_nodes_node_id ON dag_nodes(snapshot_id, node_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_nodes_level_start ON dag_nodes(snapshot_id, level, start_idx)`,

		`CREATE TABLE IF NOT EXISTS dag_edges (
    id BLOB PRIMARY KEY,
    snapshot_id BLOB NOT NULL REFERENCES dag_snapshots(id) ON DELETE CASCADE,
    parent_node_id BLOB NOT NULL REFERENCES dag_nodes(id) ON DELETE CASCADE,
    child_node_id BLOB NOT NULL REFERENCES dag_nodes(id) ON DELETE CASCADE,
    edge_index INTEGER NOT NULL DEFAULT 0,
    metadata_json JSON NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_edges_snapshot ON dag_edges(snapshot_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_edges_parent ON dag_edges(parent_node_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dag_edges_child ON dag_edges(child_node_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("011_dag_tables up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down011DAGTables(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS dag_edges`,
		`DROP TABLE IF EXISTS dag_nodes`,
		`DROP TABLE IF EXISTS dag_snapshots`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("011_dag_tables down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
