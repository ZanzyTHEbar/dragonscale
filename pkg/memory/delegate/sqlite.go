// Package delegate provides MemoryDelegate implementations backed by real databases.
package delegate

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
	memsqlc "github.com/sipeed/picoclaw/pkg/memory/sqlc"

	_ "github.com/tursodatabase/go-libsql" // register "libsql" driver
)

//go:embed fts5.sql
var fts5DDL string

//go:embed vector.sql
var vectorDDL string

// DefaultEmbeddingDims is the default number of dimensions for embedding vectors.
// This matches common models like sentence-transformers (768-dim).
const DefaultEmbeddingDims = 768

// LibSQLDelegate implements memory.MemoryDelegate using tursodatabase/go-libsql
// with sqlc-generated queries for all CRUD operations.
// Hand-written SQL (FTS5, vector search) uses a prepared statement cache.
type LibSQLDelegate struct {
	db            *sql.DB
	queries       *memsqlc.Queries
	stmts         *stmtCache
	caps          capFlags
	embeddingDims int
}

// NewLibSQLDelegate opens a libSQL database at the given path and returns
// a delegate ready for use. Call Init() to create tables.
// Uses DefaultEmbeddingDims (768) for the vector column size.
func NewLibSQLDelegate(dbPath string) (*LibSQLDelegate, error) {
	db, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open libsql: %w", err)
	}
	// Single writer for WAL mode safety
	db.SetMaxOpenConns(1)

	// Set pragmas — journal_mode returns a row, so use QueryRowContext for it.
	// go-libsql doesn't support query-string pragmas.
	ctx := context.Background()
	var walMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&walMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set foreign_keys: %w", err)
	}

	return &LibSQLDelegate{
		db:            db,
		queries:       memsqlc.New(db),
		stmts:         newStmtCache(db),
		embeddingDims: DefaultEmbeddingDims,
	}, nil
}

// NewLibSQLDelegateWithDims opens a libSQL database with a custom embedding dimension.
// Use this when your embedding model produces vectors of a non-default size
// (e.g., 384 for MiniLM, 1024 for larger models, 1536 for OpenAI ada-002).
func NewLibSQLDelegateWithDims(dbPath string, dims int) (*LibSQLDelegate, error) {
	d, err := NewLibSQLDelegate(dbPath)
	if err != nil {
		return nil, err
	}
	if dims > 0 {
		d.embeddingDims = dims
	}
	return d, nil
}

// NewLibSQLInMemory creates an in-memory libSQL delegate (useful for testing).
func NewLibSQLInMemory() (*LibSQLDelegate, error) {
	return NewLibSQLDelegate(":memory:")
}

// fts5FallbackDDL is a simplified standalone FTS5 DDL without advanced tokenizer.
// Used when the primary FTS5 DDL fails (e.g., tokenchars not supported).
const fts5FallbackDDL = `
CREATE VIRTUAL TABLE IF NOT EXISTS recall_items_fts USING fts5(
    content,
    tags
);

CREATE TRIGGER IF NOT EXISTS recall_items_ai
AFTER INSERT ON recall_items BEGIN
    INSERT INTO recall_items_fts(rowid, content, tags)
    VALUES (new.rowid, new.content, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS recall_items_ad
AFTER DELETE ON recall_items BEGIN
    DELETE FROM recall_items_fts WHERE rowid = old.rowid;
END;

CREATE TRIGGER IF NOT EXISTS recall_items_au
AFTER UPDATE ON recall_items BEGIN
    DELETE FROM recall_items_fts WHERE rowid = old.rowid;
    INSERT INTO recall_items_fts(rowid, content, tags)
    VALUES (new.rowid, new.content, new.tags);
END;
`

// execMultiStatement splits a SQL string into individual statements and
// executes each one. The go-libsql driver only handles one statement per
// ExecContext call. This function handles triggers with BEGIN...END blocks
// by tracking nesting depth.
func execMultiStatement(ctx context.Context, db *sql.DB, ddl string) error {
	stmts := splitSQL(ddl)
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("failed to execute query %s\n%w", s, err)
		}
	}
	return nil
}

// splitSQL splits multi-statement SQL into individual statements,
// correctly handling BEGIN...END blocks (triggers) that contain semicolons.
func splitSQL(ddl string) []string {
	var result []string
	var current strings.Builder
	depth := 0 // tracks BEGIN...END nesting

	for _, line := range strings.Split(ddl, "\n") {
		trimmed := strings.TrimSpace(line)

		// Skip comment-only and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			current.WriteString(line)
			current.WriteByte('\n')
			continue
		}

		upper := strings.ToUpper(trimmed)

		// Track BEGIN...END nesting for triggers.
		// BEGIN can appear at start ("BEGIN") or end of a line ("... BEGIN").
		if upper == "BEGIN" || strings.HasSuffix(upper, " BEGIN") || strings.HasSuffix(upper, "\tBEGIN") {
			depth++
		}
		if upper == "END;" || strings.HasSuffix(upper, "END;") {
			depth--
			current.WriteString(line)
			current.WriteByte('\n')
			if depth <= 0 {
				stmt := strings.TrimSpace(current.String())
				if stmt != "" {
					result = append(result, stmt)
				}
				current.Reset()
				depth = 0
			}
			continue
		}

		current.WriteString(line)
		current.WriteByte('\n')

		// If we're outside a BEGIN...END block and the line ends with ';',
		// treat it as a statement boundary.
		if depth == 0 && strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				result = append(result, stmt)
			}
			current.Reset()
		}
	}

	// Capture any trailing statement without a final semicolon
	if s := strings.TrimSpace(current.String()); s != "" {
		result = append(result, s)
	}

	return result
}

// schemaDDL generates the core DDL with the configured embedding dimensions.
// Entity IDs use BLOB PRIMARY KEY (16-byte UUIDv7). External identifiers remain TEXT.
func (d *LibSQLDelegate) schemaDDL() string {
	return fmt.Sprintf(`-- PicoClaw Memory System Schema (libSQL)
-- Entity IDs: BLOB PRIMARY KEY (16-byte UUIDv7 RFC 9562)
-- External identifiers (agent_id, session_key): TEXT
CREATE TABLE IF NOT EXISTS working_context (
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, session_key)
);
CREATE TABLE IF NOT EXISTS recall_items (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'system',
    sector TEXT NOT NULL DEFAULT 'episodic',
    importance REAL NOT NULL DEFAULT 0.5,
    salience REAL NOT NULL DEFAULT 0.5,
    decay_rate REAL NOT NULL DEFAULT 0.01,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_recall_agent_session ON recall_items(agent_id, session_key);
CREATE INDEX IF NOT EXISTS idx_recall_sector ON recall_items(sector);
CREATE INDEX IF NOT EXISTS idx_recall_importance ON recall_items(importance DESC);
CREATE INDEX IF NOT EXISTS idx_recall_created ON recall_items(created_at DESC);
CREATE TABLE IF NOT EXISTS archival_chunks (
    id BLOB PRIMARY KEY,
    recall_id BLOB NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding F32_BLOB(%d),
    source TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chunks_recall ON archival_chunks(recall_id);
CREATE INDEX IF NOT EXISTS idx_chunks_source ON archival_chunks(source);
CREATE TABLE IF NOT EXISTS memory_summaries (
    id BLOB PRIMARY KEY,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    from_msg_idx INTEGER NOT NULL DEFAULT 0,
    to_msg_idx INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_summaries_agent_session ON memory_summaries(agent_id, session_key);
CREATE TRIGGER IF NOT EXISTS recall_cascade_delete
AFTER DELETE ON recall_items BEGIN
    DELETE FROM archival_chunks WHERE recall_id = old.id;
END;`, d.embeddingDims)
}

func (d *LibSQLDelegate) Init(ctx context.Context) error {
	if err := execMultiStatement(ctx, d.db, d.schemaDDL()); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	// FTS5 virtual tables and triggers — try advanced tokenizer first,
	// fall back to basic FTS5, then skip entirely if unavailable.
	if err := execMultiStatement(ctx, d.db, fts5DDL); err != nil {
		// Advanced tokenizer failed — try simplified FTS5
		if err2 := execMultiStatement(ctx, d.db, fts5FallbackDDL); err2 != nil {
			// FTS5 not available at all — LIKE-based search will be used
			_ = err2
		}
	}

	// Backfill: ensure any existing recall_items are indexed in FTS5.
	// This is idempotent — only inserts rows not already present.
	_, _ = d.db.ExecContext(ctx,
		`INSERT INTO recall_items_fts(rowid, content, tags)
		 SELECT ri.rowid, ri.content, ri.tags
		 FROM recall_items ri
		 WHERE NOT EXISTS (SELECT 1 FROM recall_items_fts f WHERE f.rowid = ri.rowid)`)

	// Vector index -- gracefully skip if libSQL vector extension not available
	if err := execMultiStatement(ctx, d.db, vectorDDL); err != nil {
		// Not fatal: vector search will fall back to Go-side brute-force
		_ = err
	}

	// Detect runtime capabilities (FTS5, BM25, vector_top_k)
	d.detectCapabilities(ctx)

	return nil
}

// EmbeddingDims returns the configured embedding vector dimensions.
func (d *LibSQLDelegate) EmbeddingDims() int { return d.embeddingDims }

func (d *LibSQLDelegate) Close() error {
	if d.stmts != nil {
		d.stmts.close()
	}
	return d.db.Close()
}

// --- Working Context ---

func (d *LibSQLDelegate) GetWorkingContext(ctx context.Context, agentID, sessionKey string) (*memory.WorkingContext, error) {
	row, err := d.queries.GetWorkingContext(ctx, memsqlc.GetWorkingContextParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &memory.WorkingContext{
		AgentID:    row.AgentID,
		SessionKey: row.SessionKey,
		Content:    row.Content,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}

func (d *LibSQLDelegate) UpsertWorkingContext(ctx context.Context, agentID, sessionKey, content string) error {
	return d.queries.UpsertWorkingContext(ctx, memsqlc.UpsertWorkingContextParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Content:    content,
	})
}

// --- Recall Items ---

func (d *LibSQLDelegate) InsertRecallItem(ctx context.Context, item *memory.RecallItem) error {
	return d.queries.InsertRecallItem(ctx, memsqlc.InsertRecallItemParams{
		ID:         item.ID,
		AgentID:    item.AgentID,
		SessionKey: item.SessionKey,
		Role:       item.Role,
		Sector:     item.Sector,
		Importance: item.Importance,
		Salience:   item.Salience,
		DecayRate:  item.DecayRate,
		Content:    item.Content,
		Tags:       item.Tags,
	})
}

func (d *LibSQLDelegate) GetRecallItem(ctx context.Context, id ids.UUID) (*memory.RecallItem, error) {
	row, err := d.queries.GetRecallItem(ctx, memsqlc.GetRecallItemParams{ID: id})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sqlcRecallToMemory(row), nil
}

func (d *LibSQLDelegate) UpdateRecallItem(ctx context.Context, item *memory.RecallItem) error {
	return d.queries.UpdateRecallItem(ctx, memsqlc.UpdateRecallItemParams{
		ID:         item.ID,
		Role:       item.Role,
		Sector:     item.Sector,
		Importance: item.Importance,
		Salience:   item.Salience,
		DecayRate:  item.DecayRate,
		Content:    item.Content,
		Tags:       item.Tags,
	})
}

func (d *LibSQLDelegate) DeleteRecallItem(ctx context.Context, id ids.UUID) error {
	return d.queries.DeleteRecallItem(ctx, memsqlc.DeleteRecallItemParams{ID: id})
}

func (d *LibSQLDelegate) ListRecallItems(ctx context.Context, agentID, sessionKey string, limit, offset int) ([]*memory.RecallItem, error) {
	rows, err := d.queries.ListRecallItems(ctx, memsqlc.ListRecallItemsParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Off:        int64(offset),
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*memory.RecallItem, len(rows))
	for i, row := range rows {
		items[i] = sqlcRecallToMemory(row)
	}
	return items, nil
}

func (d *LibSQLDelegate) SearchRecallByKeyword(ctx context.Context, query, agentID string, limit int) ([]*memory.RecallItem, error) {
	rows, err := d.queries.SearchRecallByKeyword(ctx, memsqlc.SearchRecallByKeywordParams{
		Keyword: &query,
		AgentID: agentID,
		Lim:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*memory.RecallItem, len(rows))
	for i, row := range rows {
		items[i] = sqlcRecallToMemory(row)
	}
	return items, nil
}

// --- Archival Chunks ---

func (d *LibSQLDelegate) InsertArchivalChunk(ctx context.Context, chunk *memory.ArchivalChunk) error {
	// Embedding.Value() returns nil (SQL NULL) for empty embeddings,
	// and F32_BLOB bytes for populated ones — no manual conversion needed.
	return d.queries.InsertArchivalChunk(ctx, memsqlc.InsertArchivalChunkParams{
		ID:         chunk.ID,
		RecallID:   chunk.RecallID,
		ChunkIndex: int64(chunk.ChunkIndex),
		Content:    chunk.Content,
		Embedding:  chunk.Embedding,
		Source:     chunk.Source,
		Hash:       chunk.Hash,
	})
}

func (d *LibSQLDelegate) GetArchivalChunk(ctx context.Context, id ids.UUID) (*memory.ArchivalChunk, error) {
	row, err := d.queries.GetArchivalChunk(ctx, memsqlc.GetArchivalChunkParams{ID: id})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sqlcChunkToMemory(row), nil
}

func (d *LibSQLDelegate) ListArchivalChunks(ctx context.Context, recallID ids.UUID) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListArchivalChunks(ctx, memsqlc.ListArchivalChunksParams{RecallID: recallID})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, len(rows))
	for i, row := range rows {
		chunks[i] = sqlcChunkToMemory(row)
	}
	return chunks, nil
}

func (d *LibSQLDelegate) ListAllArchivalChunks(ctx context.Context, limit, offset int) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListAllArchivalChunks(ctx, memsqlc.ListAllArchivalChunksParams{
		Lim: int64(limit),
		Off: int64(offset),
	})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, len(rows))
	for i, row := range rows {
		chunks[i] = sqlcChunkToMemory(row)
	}
	return chunks, nil
}

func (d *LibSQLDelegate) DeleteArchivalChunks(ctx context.Context, recallID ids.UUID) error {
	return d.queries.DeleteArchivalChunksByRecall(ctx, memsqlc.DeleteArchivalChunksByRecallParams{RecallID: recallID})
}

// --- Summaries ---

func (d *LibSQLDelegate) InsertSummary(ctx context.Context, summary *memory.MemorySummary) error {
	return d.queries.InsertSummary(ctx, memsqlc.InsertSummaryParams{
		ID:         summary.ID,
		AgentID:    summary.AgentID,
		SessionKey: summary.SessionKey,
		Content:    summary.Content,
		FromMsgIdx: int64(summary.FromMsgIdx),
		ToMsgIdx:   int64(summary.ToMsgIdx),
	})
}

func (d *LibSQLDelegate) ListSummaries(ctx context.Context, agentID, sessionKey string, limit int) ([]*memory.MemorySummary, error) {
	rows, err := d.queries.ListSummaries(ctx, memsqlc.ListSummariesParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]*memory.MemorySummary, len(rows))
	for i, row := range rows {
		summaries[i] = &memory.MemorySummary{
			ID:         row.ID,
			AgentID:    row.AgentID,
			SessionKey: row.SessionKey,
			Content:    row.Content,
			FromMsgIdx: int(row.FromMsgIdx),
			ToMsgIdx:   int(row.ToMsgIdx),
			CreatedAt:  row.CreatedAt,
		}
	}
	return summaries, nil
}

// --- Stats ---

func (d *LibSQLDelegate) CountRecallItems(ctx context.Context, agentID, sessionKey string) (int, error) {
	count, err := d.queries.CountRecallItems(ctx, memsqlc.CountRecallItemsParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
	})
	return int(count), err
}

func (d *LibSQLDelegate) CountArchivalChunks(ctx context.Context) (int, error) {
	count, err := d.queries.CountArchivalChunks(ctx)
	return int(count), err
}

// --- Conversion helpers ---

func sqlcRecallToMemory(row memsqlc.RecallItem) *memory.RecallItem {
	return &memory.RecallItem{
		ID:         row.ID,
		AgentID:    row.AgentID,
		SessionKey: row.SessionKey,
		Role:       row.Role,
		Sector:     row.Sector, // already memory.Sector via sqlc override
		Importance: row.Importance,
		Salience:   row.Salience,
		DecayRate:  row.DecayRate,
		Content:    row.Content,
		Tags:       row.Tags,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func sqlcChunkToMemory(row memsqlc.ArchivalChunk) *memory.ArchivalChunk {
	return &memory.ArchivalChunk{
		ID:         row.ID,
		RecallID:   row.RecallID,
		ChunkIndex: int(row.ChunkIndex),
		Content:    row.Content,
		Embedding:  row.Embedding, // memory.Embedding with auto-deserialization via Scanner
		Source:     row.Source,
		Hash:       row.Hash,
		CreatedAt:  row.CreatedAt,
	}
}
