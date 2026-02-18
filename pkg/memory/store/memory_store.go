package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

// Config controls the MemoryStore behavior.
type Config struct {
	// ContextWindowTokens is the total context window size for pressure calculations.
	ContextWindowTokens int // Default: 128000

	// OffloadThresholdTokens is the token count above which tool results are offloaded to archival.
	OffloadThresholdTokens int // Default: 4000

	// DefaultHalfLifeHours controls recency decay for search. Default: 168 (1 week).
	DefaultHalfLifeHours float64
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ContextWindowTokens:    128000,
		OffloadThresholdTokens: 4000,
		DefaultHalfLifeHours:   168,
	}
}

// MemoryStore implements memory.Memory by composing a MemoryDelegate, EmbeddingProvider, and Chunker.
type MemoryStore struct {
	delegate memory.MemoryDelegate
	embedder memory.EmbeddingProvider // may be nil if embeddings disabled
	chunker  memory.Chunker
	cfg      Config
}

// New creates a MemoryStore.
// embedder may be nil to disable vector search (keyword-only fallback).
func New(delegate memory.MemoryDelegate, chunker memory.Chunker, embedder memory.EmbeddingProvider, cfg Config) *MemoryStore {
	if cfg.ContextWindowTokens <= 0 {
		cfg.ContextWindowTokens = 128000
	}
	if cfg.OffloadThresholdTokens <= 0 {
		cfg.OffloadThresholdTokens = 4000
	}
	if cfg.DefaultHalfLifeHours <= 0 {
		cfg.DefaultHalfLifeHours = 168
	}
	return &MemoryStore{
		delegate: delegate,
		embedder: embedder,
		chunker:  chunker,
		cfg:      cfg,
	}
}

// --- Working Context (hot tier) ---

func (m *MemoryStore) GetWorkingContext(ctx context.Context, agentID, sessionKey string) (string, error) {
	wc, err := m.delegate.GetWorkingContext(ctx, agentID, sessionKey)
	if err != nil {
		return "", err
	}
	if wc == nil {
		return "", nil
	}
	return wc.Content, nil
}

func (m *MemoryStore) SetWorkingContext(ctx context.Context, agentID, sessionKey, content string) error {
	return m.delegate.UpsertWorkingContext(ctx, agentID, sessionKey, content)
}

// --- Recall (warm tier) ---

func (m *MemoryStore) StoreRecall(ctx context.Context, item *memory.RecallItem) error {
	if item.ID.IsZero() {
		item.ID = ids.New()
	}
	return m.delegate.InsertRecallItem(ctx, item)
}

func (m *MemoryStore) GetRecall(ctx context.Context, id ids.UUID) (*memory.RecallItem, error) {
	return m.delegate.GetRecallItem(ctx, id)
}

func (m *MemoryStore) UpdateRecall(ctx context.Context, item *memory.RecallItem) error {
	return m.delegate.UpdateRecallItem(ctx, item)
}

func (m *MemoryStore) DeleteRecall(ctx context.Context, id ids.UUID) error {
	// Cascade: delete archival chunks first
	if err := m.delegate.DeleteArchivalChunks(ctx, id); err != nil {
		return fmt.Errorf("delete archival chunks: %w", err)
	}
	return m.delegate.DeleteRecallItem(ctx, id)
}

// --- Archival (cold tier) ---

// StoreArchival chunks content, embeds it, and stores it in the archival tier.
// Returns the recall item ID that groups the chunks.
func (m *MemoryStore) StoreArchival(ctx context.Context, content, source string, metadata map[string]string) (ids.UUID, error) {
	// Create a recall item as the parent (UUIDv7 for chronological sorting)
	recallID := ids.New()
	sector := memory.SectorSemantic
	if s, ok := metadata["sector"]; ok {
		sector = memory.Sector(s)
	}

	var zero ids.UUID
	recallItem := &memory.RecallItem{
		ID:         recallID,
		AgentID:    metadata["agent_id"],
		SessionKey: metadata["session_key"],
		Role:       "system",
		Sector:     sector,
		Importance: 0.5,
		Content:    truncate(content, 500),
		Tags:       metadata["tags"],
	}
	if err := m.delegate.InsertRecallItem(ctx, recallItem); err != nil {
		return zero, fmt.Errorf("insert recall item: %w", err)
	}

	// Chunk the content
	chunks, err := m.chunker.Chunk(content)
	if err != nil {
		return recallID, fmt.Errorf("chunk content: %w", err)
	}

	// Embed chunks if provider available
	var embeddings []memory.Embedding
	if m.embedder != nil && len(chunks) > 0 {
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Text
		}
		embeddings, err = m.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			// Non-fatal: store chunks without embeddings, log and continue
			embeddings = nil
		}
	}

	// Store each chunk
	for i, chunk := range chunks {
		var emb memory.Embedding
		if i < len(embeddings) {
			emb = embeddings[i]
		}
		archChunk := &memory.ArchivalChunk{
			ID:         ids.New(),
			RecallID:   recallID,
			ChunkIndex: chunk.Index,
			Content:    chunk.Text,
			Embedding:  emb,
			Source:     source,
			Hash:       hashContent(chunk.Text),
		}
		if err := m.delegate.InsertArchivalChunk(ctx, archChunk); err != nil {
			return recallID, fmt.Errorf("insert chunk %d: %w", i, err)
		}
	}

	return recallID, nil
}

// RetrieveArchival retrieves the full content of an archival item by its recall ID.
func (m *MemoryStore) RetrieveArchival(ctx context.Context, id ids.UUID) (string, error) {
	chunks, err := m.delegate.ListArchivalChunks(ctx, id)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		// Try as a direct recall item
		item, err := m.delegate.GetRecallItem(ctx, id)
		if err != nil {
			return "", err
		}
		if item != nil {
			return item.Content, nil
		}
		return "", fmt.Errorf("archival item not found: %s", id.String())
	}

	// Reassemble chunks in order
	var total int
	for _, c := range chunks {
		total += len(c.Content)
	}
	buf := make([]byte, 0, total+len(chunks))
	for i, c := range chunks {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, c.Content...)
	}
	return string(buf), nil
}

// --- Retrieval pipeline ---

func (m *MemoryStore) Search(ctx context.Context, query string, opts memory.SearchOptions) ([]memory.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	var resultSets [][]memory.SearchResult
	var weights []float64

	// 1. Keyword search (via delegate)
	kwWeight := opts.KeywordWeight
	if kwWeight <= 0 {
		kwWeight = 1.0
	}
	kwResults, err := m.keywordSearch(ctx, query, opts, limit*2) // fetch extra for fusion
	if err == nil && len(kwResults) > 0 {
		resultSets = append(resultSets, kwResults)
		weights = append(weights, kwWeight)
	}

	// 2. Vector search (if embedder available)
	vecWeight := opts.VectorWeight
	if vecWeight <= 0 {
		vecWeight = 0.8
	}
	if m.embedder != nil {
		vecResults, err := m.vectorSearch(ctx, query, opts, limit*2)
		if err == nil && len(vecResults) > 0 {
			resultSets = append(resultSets, vecResults)
			weights = append(weights, vecWeight)
		}
	}

	if len(resultSets) == 0 {
		return nil, nil
	}

	// 3. RRF fusion
	merged := ReciprocalRankFusion(resultSets, weights, 60)

	// 4. Recency decay
	halfLife := opts.HalfLifeHours
	if halfLife <= 0 {
		halfLife = m.cfg.DefaultHalfLifeHours
	}
	// Build a createdAt lookup from recall items
	createdAtMap := make(map[ids.UUID]time.Time)
	for _, r := range merged {
		item, err := m.delegate.GetRecallItem(ctx, r.ID)
		if err == nil && item != nil {
			createdAtMap[r.ID] = item.CreatedAt
		}
	}
	ApplyRecencyDecay(merged, time.Now(), halfLife, func(id ids.UUID) time.Time {
		return createdAtMap[id]
	})

	// 5. Metadata pre-filtering (sectors, session_key, date range)
	merged = m.applyMetadataFilters(ctx, merged, opts)

	// 6. Filter by min score
	if opts.MinScore > 0 {
		filtered := merged[:0]
		for _, r := range merged {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		merged = filtered
	}

	// 7. Limit
	if len(merged) > limit {
		merged = merged[:limit]
	}

	return merged, nil
}

// applyMetadataFilters removes results that don't match the requested sector,
// session_key, or date range constraints. It fetches recall item metadata
// from the delegate as needed.
func (m *MemoryStore) applyMetadataFilters(ctx context.Context, results []memory.SearchResult, opts memory.SearchOptions) []memory.SearchResult {
	needSectorFilter := len(opts.Sectors) > 0
	needSessionFilter := opts.SessionKey != ""
	needDateFilter := opts.DateAfter != nil || opts.DateBefore != nil

	if !needSectorFilter && !needSessionFilter && !needDateFilter {
		return results
	}

	// Build sector lookup set
	sectorSet := make(map[memory.Sector]bool, len(opts.Sectors))
	for _, s := range opts.Sectors {
		sectorSet[s] = true
	}

	filtered := results[:0]
	for _, r := range results {
		item, err := m.delegate.GetRecallItem(ctx, r.ID)
		if err != nil || item == nil {
			continue // skip items we can't verify
		}

		if needSessionFilter && item.SessionKey != opts.SessionKey {
			continue
		}

		if needSectorFilter && !sectorSet[item.Sector] {
			continue
		}

		if opts.DateAfter != nil && item.CreatedAt.Before(*opts.DateAfter) {
			continue
		}

		if opts.DateBefore != nil && item.CreatedAt.After(*opts.DateBefore) {
			continue
		}

		filtered = append(filtered, r)
	}

	return filtered
}

func (m *MemoryStore) keywordSearch(ctx context.Context, query string, opts memory.SearchOptions, limit int) ([]memory.SearchResult, error) {
	// Try DB-side FTS first (BM25 ranked)
	if m.delegate.HasFTS() {
		items, err := m.delegate.SearchRecallByFTS(ctx, query, opts.AgentID, limit)
		if err == nil && len(items) > 0 {
			return recallItemsToResults(items), nil
		}
	}

	// Fall back to LIKE-based keyword search
	items, err := m.delegate.SearchRecallByKeyword(ctx, query, opts.AgentID, limit)
	if err != nil {
		return nil, err
	}
	return recallItemsToResults(items), nil
}

func (m *MemoryStore) vectorSearch(ctx context.Context, query string, opts memory.SearchOptions, limit int) ([]memory.SearchResult, error) {
	queryVec, err := m.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	// Try DB-side vector search first (ANN or brute-force via libSQL)
	if m.delegate.HasVectorSearch() {
		results, err := m.delegate.SearchArchivalByVector(ctx, queryVec, limit, 0)
		if err == nil && len(results) > 0 {
			return results, nil
		}
	}

	// Fall back to Go-side brute-force cosine similarity
	return m.vectorSearchGoSide(ctx, queryVec, limit)
}

// vectorSearchGoSide performs Go-side brute-force vector search as a fallback.
func (m *MemoryStore) vectorSearchGoSide(ctx context.Context, queryVec memory.Embedding, limit int) ([]memory.SearchResult, error) {
	var allChunks []*memory.ArchivalChunk
	offset := 0
	batchSize := 5000
	for {
		batch, err := m.delegate.ListAllArchivalChunks(ctx, batchSize, offset)
		if err != nil {
			return nil, err
		}
		allChunks = append(allChunks, batch...)
		if len(batch) < batchSize {
			break
		}
		offset += batchSize
	}

	inputs := make([]VectorSearchInput, 0, len(allChunks))
	for _, chunk := range allChunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		inputs = append(inputs, VectorSearchInput{
			Chunk:     chunk,
			Embedding: chunk.Embedding,
		})
	}

	return VectorSearch(queryVec, inputs, limit), nil
}

// recallItemsToResults converts delegate recall items into search results.
func recallItemsToResults(items []*memory.RecallItem) []memory.SearchResult {
	results := make([]memory.SearchResult, len(items))
	for i, item := range items {
		results[i] = memory.SearchResult{
			ID:      item.ID,
			Content: item.Content,
			Source:  item.SessionKey,
			Score:   item.Importance,
			Sector:  item.Sector,
		}
	}
	return results
}

// --- Summaries ---

func (m *MemoryStore) StoreSummary(ctx context.Context, summary *memory.MemorySummary) error {
	if summary.ID.IsZero() {
		summary.ID = ids.New()
	}
	return m.delegate.InsertSummary(ctx, summary)
}

// --- Context pressure ---

func (m *MemoryStore) ContextUsage(ctx context.Context, agentID, sessionKey string) (*memory.ContextPressure, error) {
	wcContent, err := m.GetWorkingContext(ctx, agentID, sessionKey)
	if err != nil {
		return nil, err
	}
	wcTokens := estimateTokens(wcContent)

	recallCount, err := m.delegate.CountRecallItems(ctx, agentID, sessionKey)
	if err != nil {
		return nil, err
	}

	archivalCount, err := m.delegate.CountArchivalChunks(ctx)
	if err != nil {
		return nil, err
	}

	// Rough estimate: recall items avg ~100 tokens each
	estimatedTotal := wcTokens + (recallCount * 100)
	ratio := float64(estimatedTotal) / float64(m.cfg.ContextWindowTokens)
	if ratio > 1.0 {
		ratio = 1.0
	}

	level := memory.PressureNormal
	switch {
	case ratio > 0.85:
		level = memory.PressureFlush
	case ratio > 0.80:
		level = memory.PressureOffload
	case ratio > 0.70:
		level = memory.PressureWarn
	}

	return &memory.ContextPressure{
		WorkingContextTokens: wcTokens,
		RecallItemCount:      recallCount,
		ArchivalChunkCount:   archivalCount,
		EstimatedTotalTokens: estimatedTotal,
		UsageRatio:           ratio,
		PressureLevel:        level,
	}, nil
}

// --- Tool result offloading (through archival tier) ---

// ShouldOffload checks if content exceeds the offload threshold.
func (m *MemoryStore) ShouldOffload(content string) bool {
	return estimateTokens(content) > m.cfg.OffloadThresholdTokens
}

// OffloadToolResult stores a large tool result in the archival tier and returns
// a summary + reference ID for in-context use.
func (m *MemoryStore) OffloadToolResult(ctx context.Context, toolName, content, agentID, sessionKey string) (refID ids.UUID, summary string, err error) {
	var zero ids.UUID
	metadata := map[string]string{
		"agent_id":    agentID,
		"session_key": sessionKey,
		"tags":        "offloaded,tool:" + toolName,
		"sector":      string(memory.SectorEpisodic),
	}

	refID, err = m.StoreArchival(ctx, content, "tool:"+toolName, metadata)
	if err != nil {
		return zero, "", fmt.Errorf("offload to archival: %w", err)
	}

	tokens := estimateTokens(content)
	summary = fmt.Sprintf("[Offloaded: %d tokens from %s → ref:%s]\n%s",
		tokens, toolName, refID.String(), truncate(content, 200))

	return refID, summary, nil
}

// --- Lifecycle ---

// Sync flushes pending writes to the remote replica (Turso).
// No-op if the underlying delegate doesn't support replication.
func (m *MemoryStore) Sync() error {
	type syncer interface{ Sync() error }
	if s, ok := m.delegate.(syncer); ok {
		return s.Sync()
	}
	return nil
}

func (m *MemoryStore) Close() error {
	return m.delegate.Close()
}

// --- helpers ---

func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

func truncate(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "..."
}

func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:16])
}
