// Package memory provides a MemGPT-style 3-tier memory system for the PicoClaw agent.
//
// Architecture follows the Memory (logic) + MemoryDelegate (backend) pattern:
//   - Memory: orchestrates tiers, scoring, context pressure, retrieval pipeline
//   - MemoryDelegate: pure CRUD persistence via sqlc-generated queries
//
// Tiers:
//   - Working Context (hot): single mutable buffer per agent/session, injected into system prompt
//   - Recall (warm): scored, classified memory items with importance/salience/sector metadata
//   - Archival (cold): chunked and embedded content for vector + keyword search
package memory

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
)

// Sector classifies the type of memory for retrieval and scoring.
type Sector string

const (
	SectorEpisodic   Sector = "episodic"   // Events, conversations, interactions
	SectorSemantic   Sector = "semantic"   // Facts, knowledge, concepts
	SectorProcedural Sector = "procedural" // How-to, workflows, patterns
	SectorReflective Sector = "reflective" // Meta-observations, self-assessments
)

// --- Embedding type (F32_BLOB wire format) ---

// Embedding is a float32 vector that transparently serializes to/from
// libSQL's F32_BLOB wire format (little-endian IEEE 754 float32, 4 bytes/element).
//
// Implements driver.Valuer and sql.Scanner so sqlc-generated code handles
// the blob↔float32 conversion automatically. An empty/nil Embedding
// serializes as SQL NULL (not a 0-byte blob), which is critical for
// go-libsql's F32_BLOB vector index.
type Embedding []float32

// Value implements driver.Valuer. Returns the F32_BLOB binary representation,
// or nil (SQL NULL) when the embedding is empty.
func (e Embedding) Value() (driver.Value, error) {
	if len(e) == 0 {
		return nil, nil // SQL NULL — critical for go-libsql vector index
	}
	buf := make([]byte, len(e)*4)
	for i, f := range e {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf, nil
}

// Scan implements sql.Scanner. Decodes F32_BLOB binary data into float32 values.
func (e *Embedding) Scan(src interface{}) error {
	if src == nil {
		*e = nil
		return nil
	}
	b, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("Embedding.Scan: expected []byte, got %T", src)
	}
	if len(b) == 0 {
		*e = nil
		return nil
	}
	if len(b)%4 != 0 {
		return fmt.Errorf("Embedding.Scan: blob size %d not a multiple of 4", len(b))
	}
	result := make([]float32, len(b)/4)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	*e = result
	return nil
}

// --- Domain types ---

// RecallItem is a memory entry in the warm tier.
type RecallItem struct {
	ID         ids.UUID
	AgentID    string
	SessionKey string
	Role       string // "system", "user", "assistant", "tool"
	Sector     Sector
	Importance float64 // [0, 1]
	Salience   float64 // [0, 1]
	DecayRate  float64 // exponential decay constant
	Content    string
	Tags       string // comma-separated
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ArchivalChunk is an embedded chunk in the cold tier.
type ArchivalChunk struct {
	ID         ids.UUID
	RecallID   ids.UUID // FK to RecallItem or standalone
	ChunkIndex int
	Content    string
	Embedding  Embedding // F32_BLOB with auto-serialization via Valuer/Scanner
	Source     string
	Hash       string
	CreatedAt  time.Time
}

// WorkingContext is the hot-tier mutable buffer.
type WorkingContext struct {
	AgentID    string
	SessionKey string
	Content    string
	UpdatedAt  time.Time
}

// MemorySummary stores compacted conversation summaries.
type MemorySummary struct {
	ID         ids.UUID
	AgentID    string
	SessionKey string
	Content    string
	FromMsgIdx int
	ToMsgIdx   int
	CreatedAt  time.Time
}

// SearchResult represents a result from hybrid retrieval.
type SearchResult struct {
	ID       ids.UUID
	Content  string
	Source   string
	Score    float64
	Sector   Sector
	Metadata map[string]string
}

// --- Core interfaces ---

// Memory is the high-level logic interface for the memory system.
// It orchestrates all three tiers and the retrieval pipeline.
type Memory interface {
	// --- Working Context (hot tier) ---
	GetWorkingContext(ctx context.Context, agentID, sessionKey string) (string, error)
	SetWorkingContext(ctx context.Context, agentID, sessionKey, content string) error

	// --- Recall (warm tier) ---
	StoreRecall(ctx context.Context, item *RecallItem) error
	GetRecall(ctx context.Context, id ids.UUID) (*RecallItem, error)
	UpdateRecall(ctx context.Context, item *RecallItem) error
	DeleteRecall(ctx context.Context, id ids.UUID) error

	// --- Archival (cold tier) ---
	StoreArchival(ctx context.Context, content, source string, metadata map[string]string) (ids.UUID, error)
	RetrieveArchival(ctx context.Context, id ids.UUID) (string, error)

	// --- Retrieval pipeline ---
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)

	// --- Summaries ---
	StoreSummary(ctx context.Context, summary *MemorySummary) error

	// --- Context pressure ---
	ContextUsage(ctx context.Context, agentID, sessionKey string) (*ContextPressure, error)

	// --- Lifecycle ---
	Close() error
}

// SearchOptions controls the hybrid retrieval pipeline.
type SearchOptions struct {
	AgentID    string
	SessionKey string // empty = search all sessions
	Sectors    []Sector
	Tags       []string
	Limit      int
	MinScore   float64
	DateAfter  *time.Time
	DateBefore *time.Time

	// Weights for RRF fusion
	KeywordWeight float64 // default 1.0
	VectorWeight  float64 // default 0.8
	RecencyWeight float64 // default 0.3

	// Recency decay
	HalfLifeHours float64 // default 168 (1 week)
}

// ContextPressure reports memory usage for context window management.
type ContextPressure struct {
	WorkingContextTokens int
	RecallItemCount      int
	ArchivalChunkCount   int
	EstimatedTotalTokens int
	UsageRatio           float64 // [0, 1] — fraction of context window used
	PressureLevel        PressureLevel
}

// PressureLevel categorizes context memory pressure.
type PressureLevel string

const (
	PressureNormal  PressureLevel = "normal"  // < 70%
	PressureWarn    PressureLevel = "warn"    // 70-80%
	PressureOffload PressureLevel = "offload" // 80-85%
	PressureFlush   PressureLevel = "flush"   // > 85%
)

// --- Delegate interface (backend) ---

// MemoryDelegate is the pure storage backend for the memory system.
// Implementations wrap sqlc-generated queries. All persistence goes through here.
// The Memory logic layer composes a MemoryDelegate for its backend.
type MemoryDelegate interface {
	// Init creates tables and runs migrations.
	Init(ctx context.Context) error

	// Close releases database resources.
	Close() error

	// --- Working Context ---
	GetWorkingContext(ctx context.Context, agentID, sessionKey string) (*WorkingContext, error)
	UpsertWorkingContext(ctx context.Context, agentID, sessionKey, content string) error

	// --- Recall Items ---
	InsertRecallItem(ctx context.Context, item *RecallItem) error
	GetRecallItem(ctx context.Context, id ids.UUID) (*RecallItem, error)
	UpdateRecallItem(ctx context.Context, item *RecallItem) error
	DeleteRecallItem(ctx context.Context, id ids.UUID) error
	ListRecallItems(ctx context.Context, agentID, sessionKey string, limit, offset int) ([]*RecallItem, error)
	SearchRecallByKeyword(ctx context.Context, query, agentID string, limit int) ([]*RecallItem, error)

	// --- Advanced Search ---

	// SearchRecallByFTS performs full-text search using FTS5 MATCH with BM25 ranking.
	// Returns nil (not error) if FTS5 is not available -- caller should fall back to keyword search.
	SearchRecallByFTS(ctx context.Context, query, agentID string, limit int) ([]*RecallItem, error)

	// SearchArchivalByVector performs DB-side vector similarity search.
	// Returns nil (not error) if vector search is not available -- caller should fall back to Go-side.
	SearchArchivalByVector(ctx context.Context, queryVec Embedding, limit, offset int) ([]SearchResult, error)

	// --- Archival Chunks ---
	InsertArchivalChunk(ctx context.Context, chunk *ArchivalChunk) error
	GetArchivalChunk(ctx context.Context, id ids.UUID) (*ArchivalChunk, error)
	ListArchivalChunks(ctx context.Context, recallID ids.UUID) ([]*ArchivalChunk, error)
	ListAllArchivalChunks(ctx context.Context, limit, offset int) ([]*ArchivalChunk, error)
	DeleteArchivalChunks(ctx context.Context, recallID ids.UUID) error

	// --- Summaries ---
	InsertSummary(ctx context.Context, summary *MemorySummary) error
	ListSummaries(ctx context.Context, agentID, sessionKey string, limit int) ([]*MemorySummary, error)

	// --- Stats ---
	CountRecallItems(ctx context.Context, agentID, sessionKey string) (int, error)
	CountArchivalChunks(ctx context.Context) (int, error)

	// --- Capability Detection ---
	HasVectorSearch() bool
	HasFTS() bool
}

// --- Embedding interface ---

// EmbeddingProvider generates vector embeddings from text.
// Implementations return Embedding vectors ([]float32), matching libSQL's F32_BLOB storage.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) (Embedding, error)
	EmbedBatch(ctx context.Context, texts []string) ([]Embedding, error)
	Dimensions() int
	Model() string
}

// --- Chunker interface ---

// Chunker splits text into chunks suitable for embedding and retrieval.
type Chunker interface {
	// Chunk splits content into chunks. Returns chunks with text and metadata.
	Chunk(content string) ([]ChunkResult, error)
}

// ChunkResult is the output of a chunking operation.
type ChunkResult struct {
	Text  string
	Index int
}
