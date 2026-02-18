package delegate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// InsertRecallItemsBatch inserts a slice of RecallItems within a single
// database transaction. This reduces WAL commits from N to 1, which gives
// measurable throughput improvements for bulk writes (e.g. session import,
// initial memory load).
//
// On success, server-assigned CreatedAt/UpdatedAt timestamps are written
// back to each item (same contract as InsertRecallItem).
// On any error the transaction is rolled back; no partial writes are visible.
func (d *LibSQLDelegate) InsertRecallItemsBatch(ctx context.Context, items []*memory.RecallItem) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := d.queries.WithTx(tx)
	for _, item := range items {
		row, err := qtx.InsertRecallItem(ctx, recallItemToParams(item))
		if err != nil {
			return fmt.Errorf("insert recall item %s: %w", item.ID, err)
		}
		item.CreatedAt = row.CreatedAt
		item.UpdatedAt = row.UpdatedAt
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}

// InsertArchivalChunksBatch inserts a slice of ArchivalChunks within a single
// database transaction. StoreArchival typically creates multiple chunks per
// recall item — wrapping them in one tx avoids N separate WAL commits.
//
// On success, server-assigned CreatedAt timestamps are written back to each
// chunk. On any error the transaction is rolled back.
func (d *LibSQLDelegate) InsertArchivalChunksBatch(ctx context.Context, chunks []*memory.ArchivalChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := d.queries.WithTx(tx)
	for _, chunk := range chunks {
		row, err := qtx.InsertArchivalChunk(ctx, archivalChunkToParams(chunk))
		if err != nil {
			return fmt.Errorf("insert archival chunk %s: %w", chunk.ID, err)
		}
		chunk.CreatedAt = row.CreatedAt
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}
