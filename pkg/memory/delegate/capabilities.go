package delegate

import (
	"context"
	"time"
)

// capFlags holds the results of runtime feature detection.
// Fields are set once during Init() and read-only afterward.
type capFlags struct {
	checked    bool
	vectorTopK bool // vector_top_k() function available
	fts5       bool // FTS5 module loaded
	bm25       bool // bm25() ranking function available
}

// detectCapabilities probes the database for optional features.
// Results are cached in d.caps. Safe to call multiple times (no-op after first).
func (d *LibSQLDelegate) detectCapabilities(ctx context.Context) {
	if d.caps.checked {
		return
	}
	d.caps.checked = true

	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Probe FTS5: attempt to query the virtual table
	d.caps.fts5 = d.probeFTS5(tctx)

	// Probe BM25: only meaningful if FTS5 is available
	if d.caps.fts5 {
		d.caps.bm25 = d.probeBM25(tctx)
	}

	// Probe vector_top_k: attempt a zero-result vector query
	d.caps.vectorTopK = d.probeVectorTopK(tctx)
}

func (d *LibSQLDelegate) probeFTS5(ctx context.Context) bool {
	// Check if the FTS5 table exists by querying it with an impossible match
	_, err := d.db.ExecContext(ctx,
		"SELECT 1 FROM recall_items_fts WHERE recall_items_fts MATCH '\"__probe__\"' LIMIT 0")
	return err == nil
}

func (d *LibSQLDelegate) probeBM25(ctx context.Context) bool {
	_, err := d.db.ExecContext(ctx,
		"SELECT bm25(recall_items_fts) FROM recall_items_fts LIMIT 0")
	return err == nil
}

func (d *LibSQLDelegate) probeVectorTopK(ctx context.Context) bool {
	// Try a minimal vector_top_k query -- will fail if the function or index doesn't exist
	_, err := d.db.ExecContext(ctx,
		"SELECT id FROM vector_top_k('idx_chunks_embedding', vector32('[0]'), 1) LIMIT 0")
	return err == nil
}

// HasVectorSearch returns true if DB-side vector search (vector_top_k) is available.
func (d *LibSQLDelegate) HasVectorSearch() bool {
	return d.caps.vectorTopK
}

// HasFTS returns true if FTS5 full-text search is available.
func (d *LibSQLDelegate) HasFTS() bool {
	return d.caps.fts5
}
