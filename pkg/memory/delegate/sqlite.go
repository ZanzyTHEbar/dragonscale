// Package delegate provides MemoryDelegate implementations backed by real databases.
package delegate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memory/migrations"
	memsqlc "github.com/sipeed/picoclaw/pkg/memory/sqlc"

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
	return sqlcRecallToMemory(row), nil
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

func (d *LibSQLDelegate) GetArchivalChunk(ctx context.Context, agentID string, id ids.UUID) (*memory.ArchivalChunk, error) {
	row, err := d.queries.GetArchivalChunk(ctx, memsqlc.GetArchivalChunkParams{ID: id, AgentID: agentID})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sqlcChunkToMemory(row), nil
}

func (d *LibSQLDelegate) ListArchivalChunks(ctx context.Context, agentID string, recallID ids.UUID) ([]*memory.ArchivalChunk, error) {
	rows, err := d.queries.ListArchivalChunks(ctx, memsqlc.ListArchivalChunksParams{RecallID: recallID, AgentID: agentID, Lim: 10000})
	if err != nil {
		return nil, err
	}
	chunks := make([]*memory.ArchivalChunk, len(rows))
	for i, row := range rows {
		chunks[i] = sqlcChunkToMemory(row)
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
		chunks[i] = sqlcChunkToMemory(row)
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
		items[i] = sqlcRecallToMemory(row)
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
