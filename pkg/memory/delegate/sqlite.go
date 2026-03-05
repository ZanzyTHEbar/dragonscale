// Package delegate provides MemoryDelegate implementations backed by real databases.
package delegate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/migrations"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/pressly/goose/v3"
	libsql "github.com/tursodatabase/go-libsql"
)

// DefaultEmbeddingDims is the default number of dimensions for embedding vectors.
// This matches common models like sentence-transformers (768-dim).
const DefaultEmbeddingDims = 768

// LibSQLDelegate implements memory.MemoryDelegate using tursodatabase/go-libsql
// with sqlc-generated queries for all CRUD operations.
// Hand-written SQL (FTS5, vector search) uses a prepared statement cache.
type LibSQLDelegate struct {
	db            *sql.DB
	connector     *libsql.Connector // non-nil when using embedded replica mode
	queries       *memsqlc.Queries
	stmts         *stmtCache
	caps          capFlags
	embeddingDims int
}

// SyncConfig configures Turso embedded replica synchronization.
type SyncConfig struct {
	// SyncURL is the remote primary database URL (e.g., "libsql://mydb.turso.io").
	SyncURL string

	// AuthToken for the remote database.
	AuthToken string

	// SyncInterval is how often to auto-sync. Zero means manual sync only.
	SyncInterval time.Duration

	// EncryptionKey enables encryption-at-rest. Empty means no encryption.
	EncryptionKey string
}

// NewLibSQLDelegate opens a libSQL database at the given path and returns
// a delegate ready for use. Call Init() to create tables.
// Uses DefaultEmbeddingDims (768) for the vector column size.
func NewLibSQLDelegate(dbPath string) (*LibSQLDelegate, error) {
	db, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("open libsql: %w", err)
	}

	return newDelegateFromDB(db, nil)
}

// NewLibSQLDelegateWithSync opens a libSQL database as an embedded replica
// that syncs with a remote Turso primary. Reads are served from the local file;
// writes propagate to the remote primary and sync back.
func NewLibSQLDelegateWithSync(dbPath string, cfg SyncConfig) (*LibSQLDelegate, error) {
	opts := []libsql.Option{
		libsql.WithAuthToken(cfg.AuthToken),
	}
	if cfg.SyncInterval > 0 {
		opts = append(opts, libsql.WithSyncInterval(cfg.SyncInterval))
	}
	if cfg.EncryptionKey != "" {
		opts = append(opts, libsql.WithEncryption(cfg.EncryptionKey))
	}

	connector, err := libsql.NewEmbeddedReplicaConnector(dbPath, cfg.SyncURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("create embedded replica connector: %w", err)
	}

	db := sql.OpenDB(connector)
	d, err := newDelegateFromDB(db, connector)
	if err != nil {
		connector.Close()
		return nil, err
	}
	return d, nil
}

// Sync manually syncs the embedded replica with the remote primary.
// Returns nil if not in replica mode.
func (d *LibSQLDelegate) Sync() error {
	if d.connector == nil {
		return nil
	}
	_, err := d.connector.Sync()
	return err
}

// IsReplica returns true if this delegate is operating as an embedded replica.
func (d *LibSQLDelegate) IsReplica() bool {
	return d.connector != nil
}

func newDelegateFromDB(db *sql.DB, connector *libsql.Connector) (*LibSQLDelegate, error) {
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
		connector:     connector,
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

func (d *LibSQLDelegate) Init(ctx context.Context) error {
	// Pass embedding dimensions into goose migration context so the Go-based
	// schema migration can create F32_BLOB(N) with the configured dimension.
	mctx := migrations.WithEmbeddingDims(ctx, d.embeddingDims)

	// Go-only migrations registered via init() in the migrations package.
	// nil filesystem — no SQL files, all logic is in Go migration functions.
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.db, nil)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(mctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Detect runtime capabilities (FTS5, BM25, vector_top_k)
	d.detectCapabilities(ctx)

	return nil
}

// MigrateDown rolls back all migrations. Intended for testing only.
func (d *LibSQLDelegate) MigrateDown(ctx context.Context) error {
	mctx := migrations.WithEmbeddingDims(ctx, d.embeddingDims)
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.db, nil)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.DownTo(mctx, 0); err != nil {
		return fmt.Errorf("migration down: %w", err)
	}
	return nil
}

// EmbeddingDims returns the configured embedding vector dimensions.
func (d *LibSQLDelegate) EmbeddingDims() int { return d.embeddingDims }

// Queries returns the underlying sqlc.Queries for callers (e.g. agent package
// tests) that need direct DB access without going through the delegate API.
func (d *LibSQLDelegate) Queries() *memsqlc.Queries { return d.queries }

func (d *LibSQLDelegate) Close() error {
	if d.stmts != nil {
		d.stmts.close()
	}
	dbErr := d.db.Close()
	if d.connector != nil {
		if err := d.connector.Close(); err != nil && dbErr == nil {
			dbErr = err
		}
	}
	return dbErr
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
	_, err := d.queries.UpsertWorkingContext(ctx, memsqlc.UpsertWorkingContextParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Content:    content,
	})
	return err
}

// --- Recall Items ---

func (d *LibSQLDelegate) InsertRecallItem(ctx context.Context, item *memory.RecallItem) error {
	row, err := d.queries.InsertRecallItem(ctx, recallItemToParams(item))
	if err != nil {
		return err
	}
	item.CreatedAt = row.CreatedAt
	item.UpdatedAt = row.UpdatedAt
	return nil
}

func recallItemToParams(item *memory.RecallItem) memsqlc.InsertRecallItemParams {
	rlWeight := item.RLWeight
	if rlWeight == 0 {
		rlWeight = 1.0 // Default weight
	}
	return memsqlc.InsertRecallItemParams{
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
		RlWeight:   &rlWeight,
	}
}

func (d *LibSQLDelegate) GetRecallItem(ctx context.Context, agentID string, id ids.UUID) (*memory.RecallItem, error) {
	row, err := d.queries.GetRecallItem(ctx, memsqlc.GetRecallItemParams{ID: id, AgentID: agentID})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Inline conversion from GetRecallItemRow (no SuppressedAt in row)
	return &memory.RecallItem{
		ID:           row.ID,
		AgentID:      row.AgentID,
		SessionKey:   row.SessionKey,
		Role:         row.Role,
		Sector:       row.Sector,
		Importance:   row.Importance,
		Salience:     row.Salience,
		DecayRate:    row.DecayRate,
		Content:      row.Content,
		Tags:         row.Tags,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		SuppressedAt: nil, // GetRecallItem query filters out suppressed items
	}, nil
}

func (d *LibSQLDelegate) GetRecallItemsByIDs(ctx context.Context, agentID string, itemIDs []ids.UUID) (map[ids.UUID]*memory.RecallItem, error) {
	if len(itemIDs) == 0 {
		return make(map[ids.UUID]*memory.RecallItem), nil
	}
	rows, err := d.queries.GetRecallItemsByIDs(ctx, memsqlc.GetRecallItemsByIDsParams{
		Ids:     itemIDs,
		AgentID: agentID,
	})
	if err != nil {
		return nil, err
	}
	result := make(map[ids.UUID]*memory.RecallItem, len(rows))
	for _, row := range rows {
		// Inline conversion from GetRecallItemsByIDsRow (no SuppressedAt in row)
		result[row.ID] = &memory.RecallItem{
			ID:           row.ID,
			AgentID:      row.AgentID,
			SessionKey:   row.SessionKey,
			Role:         row.Role,
			Sector:       row.Sector,
			Importance:   row.Importance,
			Salience:     row.Salience,
			DecayRate:    row.DecayRate,
			Content:      row.Content,
			Tags:         row.Tags,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
			SuppressedAt: nil, // GetRecallItemsByIDs query filters out suppressed items
		}
	}
	return result, nil
}

func (d *LibSQLDelegate) UpdateRecallItem(ctx context.Context, item *memory.RecallItem) error {
	return d.queries.UpdateRecallItem(ctx, memsqlc.UpdateRecallItemParams{
		ID:         item.ID,
		AgentID:    item.AgentID,
		Role:       item.Role,
		Sector:     item.Sector,
		Importance: item.Importance,
		Salience:   item.Salience,
		DecayRate:  item.DecayRate,
		Content:    item.Content,
		Tags:       item.Tags,
	})
}

func (d *LibSQLDelegate) DeleteRecallItem(ctx context.Context, agentID string, id ids.UUID) error {
	return d.queries.DeleteRecallItem(ctx, memsqlc.DeleteRecallItemParams{ID: id, AgentID: agentID})
}

// SoftDeleteRecallItem sets suppressed_at instead of permanently deleting.
func (d *LibSQLDelegate) SoftDeleteRecallItem(ctx context.Context, agentID string, id ids.UUID) error {
	if err := d.queries.SoftDeleteRecallItem(ctx, memsqlc.SoftDeleteRecallItemParams{ID: id, AgentID: agentID}); err != nil {
		return err
	}
	// Also soft-delete associated archival chunks
	return d.queries.SoftDeleteArchivalChunks(ctx, memsqlc.SoftDeleteArchivalChunksParams{RecallID: id})
}

// ListQuarantinedRecallItems returns recall items ready for permanent deletion.
func (d *LibSQLDelegate) ListQuarantinedRecallItems(ctx context.Context, agentID string, cutoff time.Time, limit int) ([]*memory.RecallItem, error) {
	rows, err := d.queries.ListQuarantinedRecallItems(ctx, memsqlc.ListQuarantinedRecallItemsParams{
		BeforeDate: &cutoff,
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*memory.RecallItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, sqlcRecallToMemory(r))
	}
	return items, nil
}

// ListQuarantinedArchivalChunks returns chunks ready for permanent deletion.
func (d *LibSQLDelegate) ListQuarantinedArchivalChunks(ctx context.Context, cutoff time.Time, limit int) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListQuarantinedArchivalChunks(ctx, memsqlc.ListQuarantinedArchivalChunksParams{
		BeforeDate: &cutoff,
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, 0, len(rows))
	for _, r := range rows {
		chunks = append(chunks, sqlcChunkToMemory(r))
	}
	return chunks, nil
}

// HardDeleteRecallItem permanently deletes a recall item.
func (d *LibSQLDelegate) HardDeleteRecallItem(ctx context.Context, agentID string, id ids.UUID) error {
	return d.queries.HardDeleteRecallItem(ctx, memsqlc.HardDeleteRecallItemParams{ID: id, AgentID: agentID})
}

// HardDeleteArchivalChunks permanently deletes chunks for a recall item.
func (d *LibSQLDelegate) HardDeleteArchivalChunks(ctx context.Context, recallID ids.UUID) error {
	return d.queries.HardDeleteArchivalChunks(ctx, memsqlc.HardDeleteArchivalChunksParams{RecallID: recallID})
}

// HardDeleteChunk permanently deletes a single archival chunk by ID.
func (d *LibSQLDelegate) HardDeleteChunk(ctx context.Context, id ids.UUID) error {
	return d.queries.HardDeleteChunk(ctx, memsqlc.HardDeleteChunkParams{ID: id})
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
		// Inline conversion from ListRecallItemsRow (no SuppressedAt in row)
		items[i] = &memory.RecallItem{
			ID:           row.ID,
			AgentID:      row.AgentID,
			SessionKey:   row.SessionKey,
			Role:         row.Role,
			Sector:       row.Sector,
			Importance:   row.Importance,
			Salience:     row.Salience,
			DecayRate:    row.DecayRate,
			Content:      row.Content,
			Tags:         row.Tags,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
			SuppressedAt: nil, // ListRecallItems query filters out suppressed items
		}
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
		// Inline conversion from SearchRecallByKeywordRow (no SuppressedAt in row)
		items[i] = &memory.RecallItem{
			ID:           row.ID,
			AgentID:      row.AgentID,
			SessionKey:   row.SessionKey,
			Role:         row.Role,
			Sector:       row.Sector,
			Importance:   row.Importance,
			Salience:     row.Salience,
			DecayRate:    row.DecayRate,
			Content:      row.Content,
			Tags:         row.Tags,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
			SuppressedAt: nil, // SearchRecallByKeyword query filters out suppressed items
		}
	}
	return items, nil
}

// --- Archival Chunks ---

func (d *LibSQLDelegate) InsertArchivalChunk(ctx context.Context, chunk *memory.ArchivalChunk) error {
	// Embedding.Value() returns nil (SQL NULL) for empty embeddings,
	// and F32_BLOB bytes for populated ones — no manual conversion needed.
	row, err := d.queries.InsertArchivalChunk(ctx, archivalChunkToParams(chunk))
	if err != nil {
		return err
	}
	chunk.CreatedAt = row.CreatedAt
	return nil
}

func archivalChunkToParams(chunk *memory.ArchivalChunk) memsqlc.InsertArchivalChunkParams {
	return memsqlc.InsertArchivalChunkParams{
		ID:         chunk.ID,
		RecallID:   chunk.RecallID,
		ChunkIndex: int64(chunk.ChunkIndex),
		Content:    chunk.Content,
		Embedding:  chunk.Embedding,
		Source:     chunk.Source,
		Hash:       chunk.Hash,
	}
}

func (d *LibSQLDelegate) InsertArchivalChunkBatch(ctx context.Context, chunks []*memory.ArchivalChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		return d.InsertArchivalChunk(ctx, chunks[0])
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := d.queries.WithTx(tx)
	for _, chunk := range chunks {
		row, err := qtx.InsertArchivalChunk(ctx, archivalChunkToParams(chunk))
		if err != nil {
			return err
		}
		chunk.CreatedAt = row.CreatedAt
	}
	return tx.Commit()
}

func (d *LibSQLDelegate) GetArchivalChunk(ctx context.Context, agentID string, id ids.UUID) (*memory.ArchivalChunk, error) {
	row, err := d.queries.GetArchivalChunk(ctx, memsqlc.GetArchivalChunkParams{ID: id, AgentID: agentID})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Inline conversion from GetArchivalChunkRow (no SuppressedAt in row)
	return &memory.ArchivalChunk{
		ID:           row.ID,
		RecallID:     row.RecallID,
		ChunkIndex:   int(row.ChunkIndex),
		Content:      row.Content,
		Embedding:    row.Embedding,
		Source:       row.Source,
		Hash:         row.Hash,
		CreatedAt:    row.CreatedAt,
		SuppressedAt: nil, // GetArchivalChunk query doesn't return suppressed items
	}, nil
}

func (d *LibSQLDelegate) ListArchivalChunks(ctx context.Context, agentID string, recallID ids.UUID) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListArchivalChunks(ctx, memsqlc.ListArchivalChunksParams{RecallID: recallID, AgentID: agentID, Lim: 10000})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, len(rows))
	for i, row := range rows {
		// Inline conversion from ListArchivalChunksRow (no SuppressedAt in row)
		chunks[i] = &memory.ArchivalChunk{
			ID:           row.ID,
			RecallID:     row.RecallID,
			ChunkIndex:   int(row.ChunkIndex),
			Content:      row.Content,
			Embedding:    row.Embedding,
			Source:       row.Source,
			Hash:         row.Hash,
			CreatedAt:    row.CreatedAt,
			SuppressedAt: nil, // ListArchivalChunks query filters out suppressed items
		}
	}
	return chunks, nil
}

func (d *LibSQLDelegate) ListAllArchivalChunks(ctx context.Context, agentID string, limit, offset int) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListAllArchivalChunks(ctx, memsqlc.ListAllArchivalChunksParams{
		AgentID: agentID,
		Lim:     int64(limit),
		Off:     int64(offset),
	})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, len(rows))
	for i, row := range rows {
		// Inline conversion from ListAllArchivalChunksRow (no SuppressedAt in row)
		chunks[i] = &memory.ArchivalChunk{
			ID:           row.ID,
			RecallID:     row.RecallID,
			ChunkIndex:   int(row.ChunkIndex),
			Content:      row.Content,
			Embedding:    row.Embedding,
			Source:       row.Source,
			Hash:         row.Hash,
			CreatedAt:    row.CreatedAt,
			SuppressedAt: nil, // ListAllArchivalChunks includes all chunks
		}
	}
	return chunks, nil
}

func (d *LibSQLDelegate) DeleteArchivalChunks(ctx context.Context, recallID ids.UUID) error {
	return d.queries.DeleteArchivalChunksByRecall(ctx, memsqlc.DeleteArchivalChunksByRecallParams{RecallID: recallID})
}

// --- Summaries ---

func (d *LibSQLDelegate) InsertSummary(ctx context.Context, summary *memory.MemorySummary) error {
	row, err := d.queries.InsertSummary(ctx, memsqlc.InsertSummaryParams{
		ID:         summary.ID,
		AgentID:    summary.AgentID,
		SessionKey: summary.SessionKey,
		Content:    summary.Content,
		FromMsgIdx: int64(summary.FromMsgIdx),
		ToMsgIdx:   int64(summary.ToMsgIdx),
	})
	if err != nil {
		return err
	}
	summary.CreatedAt = row.CreatedAt
	return nil
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

func (d *LibSQLDelegate) CountArchivalChunks(ctx context.Context, agentID string) (int, error) {
	count, err := d.queries.CountArchivalChunks(ctx, memsqlc.CountArchivalChunksParams{AgentID: agentID})
	return int(count), err
}

// --- Key-Value Store ---

func (d *LibSQLDelegate) GetKV(ctx context.Context, agentID, key string) (string, error) {
	row, err := d.queries.GetKV(ctx, memsqlc.GetKVParams{
		AgentID: agentID,
		Key:     key,
	})
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func (d *LibSQLDelegate) UpsertKV(ctx context.Context, agentID, key, value string) error {
	_, err := d.queries.UpsertKV(ctx, memsqlc.UpsertKVParams{
		AgentID: agentID,
		Key:     key,
		Value:   value,
	})
	return err
}

func (d *LibSQLDelegate) DeleteKV(ctx context.Context, agentID, key string) error {
	return d.queries.DeleteKV(ctx, memsqlc.DeleteKVParams{
		AgentID: agentID,
		Key:     key,
	})
}

func (d *LibSQLDelegate) ListKVByPrefix(ctx context.Context, agentID, prefix string, limit int) (map[string]string, error) {
	rows, err := d.queries.ListKVByPrefix(ctx, memsqlc.ListKVByPrefixParams{
		AgentID: agentID,
		Prefix:  &prefix,
		Lim:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result, nil
}

// --- Documents ---

func (d *LibSQLDelegate) GetDocument(ctx context.Context, agentID, name string) (*memory.AgentDocument, error) {
	row, err := d.queries.GetDocument(ctx, memsqlc.GetDocumentParams{
		AgentID: agentID,
		Name:    name,
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sqlcDocToMemory(row), nil
}

func (d *LibSQLDelegate) UpsertDocument(ctx context.Context, doc *memory.AgentDocument) error {
	row, err := d.queries.UpsertDocument(ctx, memsqlc.UpsertDocumentParams{
		ID:       doc.ID,
		AgentID:  doc.AgentID,
		Name:     doc.Name,
		Category: doc.Category,
		Content:  doc.Content,
	})
	if err != nil {
		return err
	}
	// Back-populate server-assigned version and timestamps.
	doc.Version = int(row.Version)
	doc.CreatedAt = row.CreatedAt
	doc.UpdatedAt = row.UpdatedAt
	return nil
}

func (d *LibSQLDelegate) DeleteDocument(ctx context.Context, agentID, name string) error {
	return d.queries.DeleteDocument(ctx, memsqlc.DeleteDocumentParams{
		AgentID: agentID,
		Name:    name,
	})
}

func (d *LibSQLDelegate) ListDocumentsByCategory(ctx context.Context, agentID, category string) ([]*memory.AgentDocument, error) {
	rows, err := d.queries.ListDocumentsByCategory(ctx, memsqlc.ListDocumentsByCategoryParams{
		AgentID:  agentID,
		Category: category,
		Lim:      1000,
	})
	if err != nil {
		return nil, err
	}
	docs := make([]*memory.AgentDocument, len(rows))
	for i, row := range rows {
		docs[i] = sqlcDocToMemory(row)
	}
	return docs, nil
}

func (d *LibSQLDelegate) ListAllDocuments(ctx context.Context, agentID string) ([]*memory.AgentDocument, error) {
	rows, err := d.queries.ListAllDocuments(ctx, memsqlc.ListAllDocumentsParams{
		AgentID: agentID,
		Lim:     1000,
	})
	if err != nil {
		return nil, err
	}
	docs := make([]*memory.AgentDocument, len(rows))
	for i, row := range rows {
		docs[i] = sqlcDocToMemory(row)
	}
	return docs, nil
}

// --- Session Messages ---

func (d *LibSQLDelegate) InsertSessionMessage(ctx context.Context, agentID, sessionKey, role, content string) error {
	_, err := d.queries.InsertSessionMessage(ctx, memsqlc.InsertSessionMessageParams{
		ID:         ids.New(),
		AgentID:    agentID,
		SessionKey: sessionKey,
		Role:       role,
		Content:    content,
	})
	return err
}

func (d *LibSQLDelegate) ListSessionMessages(ctx context.Context, agentID, sessionKey, role string, limit int) ([]*memory.RecallItem, error) {
	rows, err := d.queries.ListSessionMessages(ctx, memsqlc.ListSessionMessagesParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Role:       role,
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*memory.RecallItem, len(rows))
	for i, row := range rows {
		items[i] = &memory.RecallItem{
			ID:         row.ID,
			AgentID:    row.AgentID,
			SessionKey: row.SessionKey,
			Role:       row.Role,
			Sector:     row.Sector,
			Importance: row.Importance,
			Salience:   row.Salience,
			DecayRate:  row.DecayRate,
			Content:    row.Content,
			Tags:       row.Tags,
			CreatedAt:  row.CreatedAt,
			UpdatedAt:  row.UpdatedAt,
		}
	}
	return items, nil
}

func (d *LibSQLDelegate) CountSessionMessages(ctx context.Context, agentID, sessionKey string) (int64, error) {
	return d.queries.CountSessionMessages(ctx, memsqlc.CountSessionMessagesParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
	})
}

// --- Audit Log ---

func (d *LibSQLDelegate) InsertAuditEntry(ctx context.Context, entry *memory.AuditEntry) error {
	row, err := d.queries.InsertAuditEntry(ctx, memsqlc.InsertAuditEntryParams{
		ID:         entry.ID,
		AgentID:    entry.AgentID,
		SessionKey: entry.SessionKey,
		Action:     entry.Action,
		Target:     entry.Target,
		Input:      &entry.Input,
		Output:     &entry.Output,
		DurationMs: ptrInt64(int64(entry.DurationMS)),
	})
	if err != nil {
		return err
	}
	entry.CreatedAt = row.CreatedAt
	return nil
}

func (d *LibSQLDelegate) InsertAuditEntryBatch(ctx context.Context, entries []*memory.AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) == 1 {
		return d.InsertAuditEntry(ctx, entries[0])
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := d.queries.WithTx(tx)
	for _, entry := range entries {
		row, err := qtx.InsertAuditEntry(ctx, memsqlc.InsertAuditEntryParams{
			ID:         entry.ID,
			AgentID:    entry.AgentID,
			SessionKey: entry.SessionKey,
			Action:     entry.Action,
			Target:     entry.Target,
			Input:      &entry.Input,
			Output:     &entry.Output,
			DurationMs: ptrInt64(int64(entry.DurationMS)),
		})
		if err != nil {
			return err
		}
		entry.CreatedAt = row.CreatedAt
	}
	return tx.Commit()
}

func (d *LibSQLDelegate) ListAuditEntries(ctx context.Context, agentID string, limit int) ([]*memory.AuditEntry, error) {
	rows, err := d.queries.ListAuditEntries(ctx, memsqlc.ListAuditEntriesParams{
		AgentID: agentID,
		Lim:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]*memory.AuditEntry, len(rows))
	for i, row := range rows {
		entries[i] = sqlcAuditToMemory(row)
	}
	return entries, nil
}

func (d *LibSQLDelegate) ListAuditEntriesByAction(ctx context.Context, agentID, action string, limit int) ([]*memory.AuditEntry, error) {
	rows, err := d.queries.ListAuditEntriesByAction(ctx, memsqlc.ListAuditEntriesByActionParams{
		AgentID: agentID,
		Action:  action,
		Lim:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]*memory.AuditEntry, len(rows))
	for i, row := range rows {
		entries[i] = sqlcAuditToMemory(row)
	}
	return entries, nil
}

func (d *LibSQLDelegate) CountAuditEntries(ctx context.Context, agentID string) (int, error) {
	count, err := d.queries.CountAuditEntries(ctx, memsqlc.CountAuditEntriesParams{
		AgentID: agentID,
	})
	return int(count), err
}

func (d *LibSQLDelegate) ListAuditEntriesBySession(ctx context.Context, agentID, sessionKey string, limit int) ([]*memory.AuditEntry, error) {
	rows, err := d.queries.ListAuditEntriesBySession(ctx, memsqlc.ListAuditEntriesBySessionParams{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Lim:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]*memory.AuditEntry, len(rows))
	for i, row := range rows {
		entries[i] = sqlcAuditToMemory(row)
	}
	return entries, nil
}

func (d *LibSQLDelegate) PruneOldAuditEntries(ctx context.Context, agentID string, before time.Time) error {
	return d.queries.PruneOldAuditEntries(ctx, memsqlc.PruneOldAuditEntriesParams{
		AgentID: agentID,
		Before:  before,
	})
}

func (d *LibSQLDelegate) CountAuditEntriesByAction(ctx context.Context, agentID, action string) (int, error) {
	count, err := d.queries.CountAuditEntriesByAction(ctx, memsqlc.CountAuditEntriesByActionParams{
		AgentID: agentID,
		Action:  action,
	})
	return int(count), err
}

// PersistDAG implements dag.DAGPersister. Persists DAG snapshot to storage.
func (d *LibSQLDelegate) PersistDAG(ctx context.Context, agentID, sessionKey string, snap *dag.PersistSnapshot) error {
	return dag.PersistDAG(ctx, d.db, d.queries, agentID, sessionKey, snap)
}

// --- Memory Edges (via sqlc) ---

func (d *LibSQLDelegate) InsertMemoryEdge(ctx context.Context, edge *memory.MemoryEdge) error {
	row, err := d.queries.InsertMemoryEdge(ctx, memsqlc.InsertMemoryEdgeParams{
		FromID:   edge.FromID,
		ToID:     edge.ToID,
		EdgeType: string(edge.EdgeType),
		Weight:   edge.Weight,
	})
	if err != nil {
		return err
	}
	edge.ID = row.ID
	edge.CreatedAt = row.CreatedAt
	return nil
}

func (d *LibSQLDelegate) ListMemoryEdges(ctx context.Context, memoryID ids.UUID) ([]*memory.MemoryEdge, error) {
	rows, err := d.queries.ListMemoryEdgesForItem(ctx, memsqlc.ListMemoryEdgesForItemParams{
		MemoryID: memoryID,
		Lim:      1000,
	})
	if err != nil {
		return nil, err
	}
	edges := make([]*memory.MemoryEdge, 0, len(rows))
	for _, r := range rows {
		edges = append(edges, sqlcEdgeToMemory(r))
	}
	return edges, nil
}

func (d *LibSQLDelegate) CountMemoryEdgesForItem(ctx context.Context, memoryID ids.UUID) (int, error) {
	count, err := d.queries.CountMemoryEdgesForItem(ctx, memsqlc.CountMemoryEdgesForItemParams{
		MemoryID: memoryID,
	})
	return int(count), err
}

// ListRecallItemsForConsolidation returns recall items with embeddings for similarity comparison.
// Used by the Cortex consolidation task to build the memory graph.
func (d *LibSQLDelegate) ListRecallItemsForConsolidation(ctx context.Context, agentID string, cutoff time.Time, limit int) ([]*memory.RecallItem, error) {
	rows, err := d.queries.ListRecallItemsForConsolidation(ctx, memsqlc.ListRecallItemsForConsolidationParams{
		Cutoff:  cutoff,
		AgentID: agentID,
		Lim:     int64(limit),
	})
	if err != nil {
		return nil, err
	}

	items := make([]*memory.RecallItem, 0, len(rows))
	for _, r := range rows {
		item := &memory.RecallItem{
			ID:         r.ID,
			AgentID:    r.AgentID,
			SessionKey: r.SessionKey,
			Role:       r.Role,
			Sector:     r.Sector,
			Importance: r.Importance,
			Salience:   r.Salience,
			DecayRate:  r.DecayRate,
			Content:    r.Content,
			Tags:       r.Tags,
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
			Embedding:  r.Embedding,
		}
		items = append(items, item)
	}
	return items, nil
}

// --- RL (Reinforcement Learning) Store Methods ---

// GetTaskBaseline retrieves the baseline statistics for an agent.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) GetTaskBaseline(ctx context.Context, agentID string) (*TaskBaseline, error) {
	row, err := d.queries.GetTaskBaseline(ctx, memsqlc.GetTaskBaselineParams{AgentID: agentID})
	if err == sql.ErrNoRows {
		// Return nil baseline for new agents - cold start handling
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	baseline := &TaskBaseline{}
	if row.Count != nil {
		baseline.Count = int(*row.Count)
	}
	if row.MeanTokens != nil {
		baseline.MeanTokens = float64(*row.MeanTokens)
	}
	if row.MeanErrors != nil {
		baseline.MeanErrors = *row.MeanErrors
	}
	if row.MeanUserCorrections != nil {
		baseline.MeanUserCorrections = *row.MeanUserCorrections
	}
	if row.M2Tokens != nil {
		baseline.M2Tokens = *row.M2Tokens
	}
	if row.M2Errors != nil {
		baseline.M2Errors = *row.M2Errors
	}
	if row.M2UserCorrections != nil {
		baseline.M2UserCorrections = *row.M2UserCorrections
	}
	return baseline, nil
}

// UpdateTaskBaseline saves the baseline statistics for an agent.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) UpdateTaskBaseline(ctx context.Context, agentID string, baseline *TaskBaseline) error {
	count := int64(baseline.Count)
	meanTokens := int64(baseline.MeanTokens)
	meanErrors := baseline.MeanErrors
	meanUserCorrections := baseline.MeanUserCorrections
	m2Tokens := baseline.M2Tokens
	m2Errors := baseline.M2Errors
	m2UserCorrections := baseline.M2UserCorrections

	return d.queries.UpdateTaskBaseline(ctx, memsqlc.UpdateTaskBaselineParams{
		AgentID:             agentID,
		Count:               &count,
		MeanTokens:          &meanTokens,
		MeanErrors:          &meanErrors,
		MeanUserCorrections: &meanUserCorrections,
		M2Tokens:            &m2Tokens,
		M2Errors:            &m2Errors,
		M2UserCorrections:   &m2UserCorrections,
	})
}

// UpdateMemoryWeight updates the RL weight and credit for a specific memory.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) UpdateMemoryWeight(ctx context.Context, memoryID ids.UUID, weight, credit float64) error {
	rlWeight := weight
	rlCredit := credit
	row, err := d.queries.GetRecallItemByID(ctx, memsqlc.GetRecallItemByIDParams{
		ID: memoryID,
	})
	if err == sql.ErrNoRows {
		return fmt.Errorf("memory item not found: %s", memoryID)
	}
	if err != nil {
		return err
	}
	return d.queries.UpdateMemoryWeight(ctx, memsqlc.UpdateMemoryWeightParams{
		RlWeight: &rlWeight,
		RlCredit: &rlCredit,
		ID:       memoryID,
		AgentID:  row.AgentID,
	})
}

// UpdateMemorySelfReport updates the self-reported score for a memory.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) UpdateMemorySelfReport(ctx context.Context, memoryID ids.UUID, score int) error {
	selfReportScore := int64(score)
	row, err := d.queries.GetRecallItemByID(ctx, memsqlc.GetRecallItemByIDParams{
		ID: memoryID,
	})
	if err == sql.ErrNoRows {
		return fmt.Errorf("memory item not found: %s", memoryID)
	}
	if err != nil {
		return err
	}
	return d.queries.UpdateMemorySelfReportScore(ctx, memsqlc.UpdateMemorySelfReportScoreParams{
		SelfReportScore: &selfReportScore,
		ID:              memoryID,
		AgentID:         row.AgentID,
	})
}

// GetCompletedTasks returns tasks completed since the given time for a specific agent.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) GetCompletedTasks(ctx context.Context, agentID string, since time.Time) ([]TaskRecord, error) {
	rows, err := d.queries.GetCompletedTasks(ctx, memsqlc.GetCompletedTasksParams{
		AgentID: agentID,
		Since:   since,
	})
	if err != nil {
		logger.WarnCF("memory", "Failed to get completed tasks", map[string]interface{}{
			"agent_id": agentID,
			"since":    since.String(),
			"error":    err.Error(),
		})
		return nil, fmt.Errorf("get completed tasks: %w", err)
	}

	tasks := make([]TaskRecord, 0, len(rows))
	for _, task := range rows {
		record := TaskRecord{
			ID:          task.ID.String(),
			Description: task.Description,
			Completed:   task.Completed,
			CreatedAt:   task.CreatedAt,
		}
		if task.TokensUsed != nil {
			record.TokensUsed = int(*task.TokensUsed)
		}
		if task.ToolCalls != nil {
			record.ToolCalls = int(*task.ToolCalls)
		}
		if task.Errors != nil {
			record.Errors = int(*task.Errors)
		}
		if task.UserCorrections != nil {
			record.UserCorrections = int(*task.UserCorrections)
		}
		tasks = append(tasks, record)
	}

	return tasks, nil
}

// GetRetrievedMemories returns memories retrieved during a task.
// Implements cortex.RLStore interface.
func (d *LibSQLDelegate) GetRetrievedMemories(ctx context.Context, taskID string) ([]RetrievedMemoryRecord, error) {
	parsedID, err := ids.Parse(taskID)
	if err != nil {
		return nil, fmt.Errorf("invalid task id: %w", err)
	}

	rows, err := d.queries.GetRetrievedMemories(ctx, memsqlc.GetRetrievedMemoriesParams{
		TaskID: parsedID,
	})
	if err != nil {
		return nil, err
	}

	records := make([]RetrievedMemoryRecord, len(rows))
	for i, row := range rows {
		records[i] = RetrievedMemoryRecord{
			MemoryID:   row.MemoryID,
			Similarity: row.Similarity,
		}
		if row.SelfReportScore != nil {
			score := int(*row.SelfReportScore)
			records[i].SelfReportScore = &score
		}
	}

	return records, nil
}

// ListActiveAgents returns all agent IDs that have completed tasks since the given time.
// Implements cortex.RLStore interface for multi-agent support.
func (d *LibSQLDelegate) ListActiveAgents(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := d.queries.ListActiveAgents(ctx, memsqlc.ListActiveAgentsParams{
		Since: since,
	})
	if err != nil {
		logger.WarnCF("memory", "Failed to list active agents", map[string]interface{}{
			"since": since,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("list active agents: %w", err)
	}
	return rows, nil
}

// --- Audit Analysis Store Methods ---

// GetRecentAuditEntries returns audit entries since the given time.
// Implements cortex.AuditAnalysisStore interface.
func (d *LibSQLDelegate) GetRecentAuditEntries(ctx context.Context, since time.Time) ([]AuditEntry, error) {
	cutoff := since.UTC()
	if cutoff.IsZero() {
		cutoff = time.Unix(0, 0).UTC()
	}

	const pageSize int64 = 1000
	offset := int64(0)
	entries := make([]AuditEntry, 0, pageSize)

	for {
		rows, err := d.queries.ListAuditEntriesGlobalSincePaged(ctx, memsqlc.ListAuditEntriesGlobalSincePagedParams{
			Since: cutoff,
			Lim:   pageSize,
			Off:   offset,
		})
		if err != nil {
			return nil, fmt.Errorf("list audit entries since: %w", err)
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			lowerAction := strings.ToLower(strings.TrimSpace(row.Action))
			toolName := strings.TrimSpace(row.Action)
			if strings.HasPrefix(lowerAction, "tool_") && strings.TrimSpace(row.Target) != "" {
				toolName = strings.TrimSpace(row.Target)
			}
			if toolName == "" {
				toolName = strings.TrimSpace(row.Target)
			}

			success := true
			if lowerAction == "tool_error" || strings.Contains(lowerAction, "error") || strings.Contains(lowerAction, "fail") {
				success = false
			}

			entry := AuditEntry{
				ID:        row.ID.String(),
				Timestamp: row.CreatedAt,
				ToolName:  toolName,
				ToolInput: "",
				Success:   success,
				SessionID: row.SessionKey,
				AgentID:   row.AgentID,
			}
			if row.Input != nil {
				entry.ToolInput = *row.Input
			}
			if !success && row.Output != nil {
				entry.ErrorMsg = *row.Output
			}
			entries = append(entries, entry)
		}

		if len(rows) < int(pageSize) {
			break
		}
		offset += int64(len(rows))
	}

	return entries, nil
}

// StoreDetectedPattern stores a detected pattern as a recall item.
// Implements cortex.AuditAnalysisStore interface.
func (d *LibSQLDelegate) StoreDetectedPattern(ctx context.Context, pattern DetectedPattern) error {
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    pattern.AgentID,
		SessionKey: pattern.SessionID,
		Role:       "system",
		Sector:     memory.SectorReflective,
		Importance: pattern.Weight,
		Salience:   pattern.Weight,
		Content:    pattern.Description,
		Tags:       fmt.Sprintf("audit,%s,%s", pattern.Type, pattern.Category),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	return d.InsertRecallItem(ctx, item)
}

// GetHighTokenSessions returns sessions with token usage above threshold.
// Implements cortex.AuditAnalysisStore interface.
func (d *LibSQLDelegate) GetHighTokenSessions(ctx context.Context, minTokens int64) ([]SessionSummary, error) {
	min := minTokens
	rows, err := d.queries.GetHighTokenSessions(ctx, memsqlc.GetHighTokenSessionsParams{
		MinTokens: &min,
		Lim:       100,
	})
	if err != nil {
		return nil, fmt.Errorf("get high token sessions: %w", err)
	}

	summaries := make([]SessionSummary, 0, len(rows))
	for _, row := range rows {
		totalTokens := int64(0)
		if row.TotalTokens != nil {
			totalTokens = int64(*row.TotalTokens)
		}
		summaries = append(summaries, SessionSummary{
			SessionID:   row.SessionID.String(),
			AgentID:     row.AgentID,
			TotalTokens: totalTokens,
			ToolCounts:  map[string]int{},
		})
	}

	return summaries, nil
}

// --- Batch Operations for Cortex Tasks (via sqlc) ---

// DecayRecallImportance applies multiplicative decay to the oldest recall items
// whose importance exceeds the floor. Uses sqlc-generated query string with
// raw ExecContext to preserve RowsAffected for observability.
func (d *LibSQLDelegate) DecayRecallImportance(ctx context.Context, factor, floor float64, batchSize int) (int64, error) {
	result, err := d.db.ExecContext(ctx, memsqlc.DecayRecallImportanceBatch, factor, floor, int64(batchSize))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CountArchivalChunksWithoutEmbedding returns the count of chunks with NULL embeddings.
func (d *LibSQLDelegate) CountArchivalChunksWithoutEmbedding(ctx context.Context) (int, error) {
	count, err := d.queries.CountArchivalChunksWithoutEmbedding(ctx)
	return int(count), err
}

// BackfillArchivalEmbeddings finds chunks without embeddings, calls embedFn for each,
// and writes the embedding back via sqlc UpdateArchivalChunkEmbedding.
func (d *LibSQLDelegate) BackfillArchivalEmbeddings(ctx context.Context, batchSize int, embedFn func(ctx context.Context, text string) ([]float32, error)) (int, error) {
	chunks, err := d.queries.ListArchivalChunksWithoutEmbedding(ctx, memsqlc.ListArchivalChunksWithoutEmbeddingParams{
		Lim: int64(batchSize),
	})
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, c := range chunks {
		vec, err := embedFn(ctx, c.Content)
		if err != nil {
			logger.WarnCF("cortex", "Embedding failed for chunk", map[string]interface{}{
				"chunk_id": c.ID.String(),
				"error":    err.Error(),
			})
			continue
		}
		emb := memory.Embedding(vec)
		if err := d.queries.UpdateArchivalChunkEmbedding(ctx, memsqlc.UpdateArchivalChunkEmbeddingParams{
			Embedding: emb,
			ID:        c.ID,
		}); err != nil {
			logger.WarnCF("cortex", "Failed to update chunk embedding", map[string]interface{}{
				"chunk_id": c.ID.String(),
				"error":    err.Error(),
			})
			continue
		}
		processed++
	}
	return processed, nil
}

// --- Immutable Messages (via sqlc) ---

func (d *LibSQLDelegate) InsertImmutableMessage(ctx context.Context, msg *memory.ImmutableMessage) error {
	row, err := d.queries.InsertImmutableMessage(ctx, memsqlc.InsertImmutableMessageParams{
		ID:            msg.ID,
		SessionKey:    msg.SessionKey,
		Role:          msg.Role,
		Content:       msg.Content,
		ToolCallID:    msg.ToolCallID,
		ToolCalls:     msg.ToolCalls,
		TokenEstimate: int64(msg.TokenEstimate),
	})
	if err != nil {
		return err
	}
	msg.CreatedAt = row.CreatedAt
	return nil
}

func (d *LibSQLDelegate) GetImmutableMessage(ctx context.Context, id ids.UUID) (*memory.ImmutableMessage, error) {
	row, err := d.queries.GetImmutableMessage(ctx, memsqlc.GetImmutableMessageParams{ID: id})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sqlcImmutableToMemory(row), nil
}

func (d *LibSQLDelegate) ListImmutableMessages(ctx context.Context, sessionKey string, limit, offset int) ([]*memory.ImmutableMessage, error) {
	rows, err := d.queries.ListImmutableMessages(ctx, memsqlc.ListImmutableMessagesParams{
		SessionKey: sessionKey,
		Lim:        int64(limit),
		Off:        int64(offset),
	})
	if err != nil {
		return nil, err
	}
	msgs := make([]*memory.ImmutableMessage, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, sqlcImmutableToMemory(r))
	}
	return msgs, nil
}

// --- Conversion helpers ---

func sqlcRecallToMemory(row memsqlc.ListQuarantinedRecallItemsRow) *memory.RecallItem {
	return &memory.RecallItem{
		ID:           row.ID,
		AgentID:      row.AgentID,
		SessionKey:   row.SessionKey,
		Role:         row.Role,
		Sector:       row.Sector, // already memory.Sector via sqlc override
		Importance:   row.Importance,
		Salience:     row.Salience,
		DecayRate:    row.DecayRate,
		Content:      row.Content,
		Tags:         row.Tags,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		SuppressedAt: row.SuppressedAt,
	}
}

func sqlcChunkToMemory(row memsqlc.ArchivalChunk) *memory.ArchivalChunk {
	return &memory.ArchivalChunk{
		ID:           row.ID,
		RecallID:     row.RecallID,
		ChunkIndex:   int(row.ChunkIndex),
		Content:      row.Content,
		Embedding:    row.Embedding, // memory.Embedding with auto-deserialization via Scanner
		Source:       row.Source,
		Hash:         row.Hash,
		CreatedAt:    row.CreatedAt,
		SuppressedAt: row.SuppressedAt,
	}
}

func sqlcImmutableToMemory(row memsqlc.ImmutableMessage) *memory.ImmutableMessage {
	return &memory.ImmutableMessage{
		ID:            row.ID,
		SessionKey:    row.SessionKey,
		Role:          row.Role,
		Content:       row.Content,
		ToolCallID:    row.ToolCallID,
		ToolCalls:     row.ToolCalls,
		TokenEstimate: int(row.TokenEstimate),
		CreatedAt:     row.CreatedAt,
	}
}

func sqlcEdgeToMemory(row memsqlc.MemoryEdge) *memory.MemoryEdge {
	return &memory.MemoryEdge{
		ID:        row.ID,
		FromID:    row.FromID,
		ToID:      row.ToID,
		EdgeType:  memory.EdgeType(row.EdgeType),
		Weight:    row.Weight,
		CreatedAt: row.CreatedAt,
	}
}

func sqlcDocToMemory(row memsqlc.AgentDocument) *memory.AgentDocument {
	return &memory.AgentDocument{
		ID:        row.ID,
		AgentID:   row.AgentID,
		Name:      row.Name,
		Category:  row.Category,
		Content:   row.Content,
		Version:   int(row.Version),
		IsActive:  row.IsActive,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func sqlcAuditToMemory(row memsqlc.AgentAuditLog) *memory.AuditEntry {
	entry := &memory.AuditEntry{
		ID:         row.ID,
		AgentID:    row.AgentID,
		SessionKey: row.SessionKey,
		Action:     row.Action,
		Target:     row.Target,
		CreatedAt:  row.CreatedAt,
	}
	if row.Input != nil {
		entry.Input = *row.Input
	}
	if row.Output != nil {
		entry.Output = *row.Output
	}
	if row.DurationMs != nil {
		entry.DurationMS = int(*row.DurationMs)
	}
	return entry
}

func ptrInt64(v int64) *int64 { return &v }
