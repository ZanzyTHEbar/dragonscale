package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up003VectorIndex, down003VectorIndex)
}

func up003VectorIndex(ctx context.Context, tx *sql.Tx) error {
	// libSQL's native vector index for ANN search. Gracefully skip if the
	// vector extension is unavailable — the delegate falls back to Go-side
	// brute-force cosine similarity.
	_, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_chunks_embedding ON archival_chunks(libsql_vector_idx(embedding))`)
	if err != nil {
		// Not fatal — vector search degrades to Go-side
		return nil
	}
	return nil
}

func down003VectorIndex(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_chunks_embedding`)
	if err != nil {
		return fmt.Errorf("003_vector_index down: %w", err)
	}
	return nil
}
