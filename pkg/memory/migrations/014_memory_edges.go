package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up014MemoryEdges, down014MemoryEdges)
}

func up014MemoryEdges(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memory_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id BLOB NOT NULL,
    to_id BLOB NOT NULL,
    edge_type TEXT NOT NULL DEFAULT 'related_to',
    weight REAL NOT NULL DEFAULT 1.0,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_edges_from ON memory_edges(from_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_edges_to ON memory_edges(to_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_edges_type ON memory_edges(edge_type)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_edges_pair ON memory_edges(from_id, to_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("014_memory_edges up: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

func down014MemoryEdges(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP INDEX IF EXISTS idx_memory_edges_pair`,
		`DROP INDEX IF EXISTS idx_memory_edges_type`,
		`DROP INDEX IF EXISTS idx_memory_edges_to`,
		`DROP INDEX IF EXISTS idx_memory_edges_from`,
		`DROP TABLE IF EXISTS memory_edges`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("014_memory_edges down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
