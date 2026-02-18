package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(up002FTS5, down002FTS5)
}

const fts5AdvancedDDL = `CREATE VIRTUAL TABLE IF NOT EXISTS recall_items_fts USING fts5(
    content,
    tags,
    tokenize = 'unicode61 tokenchars=:-_@./',
    prefix = '2 3 4 5 6 7'
)`

const fts5BasicDDL = `CREATE VIRTUAL TABLE IF NOT EXISTS recall_items_fts USING fts5(
    content,
    tags
)`

var fts5Triggers = []string{
	`CREATE TRIGGER IF NOT EXISTS recall_items_ai
AFTER INSERT ON recall_items BEGIN
    INSERT INTO recall_items_fts(rowid, content, tags)
    VALUES (new.rowid, new.content, new.tags);
END`,
	`CREATE TRIGGER IF NOT EXISTS recall_items_ad
AFTER DELETE ON recall_items BEGIN
    DELETE FROM recall_items_fts WHERE rowid = old.rowid;
END`,
	`CREATE TRIGGER IF NOT EXISTS recall_items_au
AFTER UPDATE ON recall_items BEGIN
    DELETE FROM recall_items_fts WHERE rowid = old.rowid;
    INSERT INTO recall_items_fts(rowid, content, tags)
    VALUES (new.rowid, new.content, new.tags);
END`,
}

func up002FTS5(ctx context.Context, tx *sql.Tx) error {
	// Try advanced tokenizer first, fall back to basic FTS5.
	// If FTS5 is entirely unavailable (e.g., stripped build), skip silently —
	// the delegate's capability detection will handle graceful degradation.
	if _, err := tx.ExecContext(ctx, fts5AdvancedDDL); err != nil {
		if _, err2 := tx.ExecContext(ctx, fts5BasicDDL); err2 != nil {
			// FTS5 not available — skip, don't fail the migration
			return nil
		}
	}

	for _, s := range fts5Triggers {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("002_fts5 trigger: %w\nSQL: %s", err, s)
		}
	}

	// Backfill any existing recall_items into the FTS index
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO recall_items_fts(rowid, content, tags)
		 SELECT ri.rowid, ri.content, ri.tags
		 FROM recall_items ri
		 WHERE NOT EXISTS (SELECT 1 FROM recall_items_fts f WHERE f.rowid = ri.rowid)`)

	return nil
}

func down002FTS5(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`DROP TRIGGER IF EXISTS recall_items_au`,
		`DROP TRIGGER IF EXISTS recall_items_ad`,
		`DROP TRIGGER IF EXISTS recall_items_ai`,
		`DROP TABLE IF EXISTS recall_items_fts`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("002_fts5 down: %w\nSQL: %s", err, s)
		}
	}
	return nil
}
