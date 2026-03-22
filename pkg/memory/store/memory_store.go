package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
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
	delegate          memory.MemoryDelegate
	embedder          memory.EmbeddingProvider // may be nil if embeddings disabled
	chunker           memory.Chunker
	cfg               Config
	agentID           string
	retrievalPolicyMu sync.Mutex

	policyCache policyCacheState
	vecCache    vectorCache
}

// vectorCache holds an in-memory copy of archival chunk embeddings so that
// vectorSearchGoSide doesn't re-scan the entire DB on every search call.
// Populated lazily on first search; updated incrementally in StoreArchival.
type vectorCache struct {
	mu     sync.RWMutex
	items  []VectorSearchInput
	loaded bool
}

// policyCacheState holds in-memory copies of retrieval policy data to avoid
// 5 KV round-trips per search call. Flushed on Sync/Close or every N queries.
type policyCacheState struct {
	state      retrievalPolicyState
	gates      retrievalPromotionGates
	metrics    retrievalShadowMetrics
	loaded     bool
	dirty      bool
	flushEvery int
	queryCount int
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
		policyCache: policyCacheState{
			flushEvery: 10,
		},
	}
}

// Embedder returns the configured EmbeddingProvider, or nil if embeddings are disabled.
func (m *MemoryStore) Embedder() memory.EmbeddingProvider { return m.embedder }

// SetAgentID sets the agent identity used to scope all memory operations.
// Invalidates the vector cache since chunks are agent-scoped.
func (m *MemoryStore) SetAgentID(agentID string) {
	m.agentID = agentID
	m.invalidateVecCache()
}

// invalidateVecCache forces the next vector search to reload from DB.
func (m *MemoryStore) invalidateVecCache() {
	m.vecCache.mu.Lock()
	m.vecCache.items = nil
	m.vecCache.loaded = false
	m.vecCache.mu.Unlock()
}

// ensurePolicyCacheLoaded initializes the in-memory retrieval policy cache
// from KV on the first access. Must be called with retrievalPolicyMu held.
func (m *MemoryStore) ensurePolicyCacheLoaded(ctx context.Context) {
	if m.policyCache.loaded {
		return
	}
	m.policyCache.state = m.loadRetrievalPolicyState(ctx)
	m.policyCache.gates = m.loadRetrievalPromotionGates(ctx)
	m.policyCache.metrics = m.loadRetrievalShadowMetrics(ctx)
	m.policyCache.loaded = true
	m.policyCache.dirty = false
	m.policyCache.queryCount = 0
}

// flushPolicyCacheLocked persists cached policy state to KV.
// Must be called with retrievalPolicyMu held.
func (m *MemoryStore) flushPolicyCacheLocked(ctx context.Context) {
	if !m.policyCache.dirty {
		return
	}
	_ = m.persistRetrievalPolicyState(ctx, m.policyCache.state)
	_ = m.persistRetrievalShadowMetrics(ctx, m.policyCache.metrics)
	m.policyCache.dirty = false
	m.policyCache.queryCount = 0
}

// FlushPolicyCache persists any dirty retrieval policy state. Safe to call
// from Sync/Close paths.
func (m *MemoryStore) FlushPolicyCache(ctx context.Context) {
	m.retrievalPolicyMu.Lock()
	defer m.retrievalPolicyMu.Unlock()
	m.flushPolicyCacheLocked(ctx)
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
	// Set initial RL weight based on category if not already set
	if item.RLWeight == 0 {
		item.RLWeight = m.initialWeightByCategory(item.Category)
	}
	return m.delegate.InsertRecallItem(ctx, item)
}

// initialWeightByCategory returns the initial RL weight based on memory category.
// Higher weights are assigned to categories that indicate higher value memories.
func (m *MemoryStore) initialWeightByCategory(category memory.Category) float64 {
	switch category {
	case memory.CategoryCorrection:
		return 1.5 // High priority - corrections are valuable
	case memory.CategoryDiscovery:
		return 1.3 // Good insights - discoveries are useful
	case memory.CategoryUserInput:
		return 2.5 // User corrections highest priority
	case memory.CategoryInsight:
		return 1.1 // Slightly above baseline
	case memory.CategoryFact:
		return 1.0 // Baseline weight
	default:
		return 1.0 // Unknown category defaults to baseline
	}
}

func (m *MemoryStore) GetRecall(ctx context.Context, id ids.UUID) (*memory.RecallItem, error) {
	return m.delegate.GetRecallItem(ctx, m.agentID, id)
}

func (m *MemoryStore) UpdateRecall(ctx context.Context, item *memory.RecallItem) error {
	return m.delegate.UpdateRecallItem(ctx, item)
}

func (m *MemoryStore) DeleteRecall(ctx context.Context, id ids.UUID) error {
	if err := m.delegate.DeleteArchivalChunks(ctx, id); err != nil {
		return fmt.Errorf("delete archival chunks: %w", err)
	}
	m.invalidateVecCache()
	return m.delegate.DeleteRecallItem(ctx, m.agentID, id)
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

	// Build all chunks, then batch-insert in a single transaction.
	stored := make([]*memory.ArchivalChunk, len(chunks))
	for i, chunk := range chunks {
		var emb memory.Embedding
		if i < len(embeddings) {
			emb = embeddings[i]
		}
		stored[i] = &memory.ArchivalChunk{
			ID:         ids.New(),
			RecallID:   recallID,
			ChunkIndex: chunk.Index,
			Content:    chunk.Text,
			Embedding:  emb,
			Source:     source,
			Hash:       hashContent(chunk.Text),
		}
	}

	if err := m.delegate.InsertArchivalChunkBatch(ctx, stored); err != nil {
		return recallID, fmt.Errorf("insert chunks: %w", err)
	}

	m.appendToVecCache(stored)
	return recallID, nil
}

// RetrieveArchival retrieves the full content of an archival item by its recall ID.
func (m *MemoryStore) RetrieveArchival(ctx context.Context, id ids.UUID) (string, error) {
	chunks, err := m.delegate.ListArchivalChunks(ctx, m.agentID, id)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		item, err := m.delegate.GetRecallItem(ctx, m.agentID, id)
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

// ReadByID loads the content of a memory entry by its UUID string.
// Tries archival chunks first, then falls back to recall item.
func (m *MemoryStore) ReadByID(ctx context.Context, agentID, idStr string) (string, error) {
	id, err := ids.Parse(idStr)
	if err != nil {
		return "", fmt.Errorf("invalid ID: %w", err)
	}
	return m.RetrieveArchival(ctx, id)
}

// --- Retrieval pipeline ---

func (m *MemoryStore) Search(ctx context.Context, query string, opts memory.SearchOptions) ([]memory.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	var baselineSets [][]memory.SearchResult
	var baselineWeights []float64

	kwWeight := opts.KeywordWeight
	vecWeight := opts.VectorWeight
	if kwWeight == 0 && vecWeight == 0 {
		kwWeight = 1.0
		vecWeight = 0.8
	}
	if kwWeight < 0 {
		kwWeight = 0
	}

	if vecWeight < 0 {
		vecWeight = 0
	}

	// 1. Keyword search (via delegate)
	if kwWeight > 0 {
		kwResults, err := m.keywordSearch(ctx, query, opts, limit*2) // fetch extra for fusion
		if err == nil && len(kwResults) > 0 {
			baselineSets = append(baselineSets, kwResults)
			baselineWeights = append(baselineWeights, kwWeight)
		}
	}

	// 2. Vector search (if embedder available)
	if vecWeight > 0 && m.embedder != nil {
		vecResults, err := m.vectorSearch(ctx, query, opts, limit*2)
		if err == nil && len(vecResults) > 0 {
			baselineSets = append(baselineSets, vecResults)
			baselineWeights = append(baselineWeights, vecWeight)
		}
	}

	var baselineFinal []memory.SearchResult
	if len(baselineSets) > 0 {
		baselineMerged := ReciprocalRankFusion(baselineSets, baselineWeights, 60)
		baselineFinal = m.postProcessMergedResults(ctx, baselineMerged, opts, limit)
	}

	// 3. Projection-aware retrieval (working-context + DAG summaries)
	hybridWeight := opts.RecencyWeight
	if hybridWeight <= 0 {
		hybridWeight = 0.6
	}

	hybridResults, err := m.hybridProjectionSearch(ctx, query, opts, limit*2)
	augmentedFinal := baselineFinal
	augmentedUsed := false
	if err == nil && len(hybridResults) > 0 {
		augmentedSets := append([][]memory.SearchResult{}, baselineSets...)
		augmentedWeights := append([]float64{}, baselineWeights...)
		augmentedSets = append(augmentedSets, hybridResults)
		augmentedWeights = append(augmentedWeights, hybridWeight)

		augmentedMerged := ReciprocalRankFusion(augmentedSets, augmentedWeights, 60)
		augmentedFinal = m.postProcessMergedResults(ctx, augmentedMerged, opts, limit)
		if len(augmentedFinal) > 0 {
			augmentedUsed = true
		}
	}

	if len(baselineFinal) == 0 && len(augmentedFinal) == 0 {
		return nil, nil
	}

	m.retrievalPolicyMu.Lock()
	m.ensurePolicyCacheLoaded(ctx)
	parity := computeRetrievalParity(baselineFinal, augmentedFinal, limit)
	m.policyCache.state, m.policyCache.metrics = m.updateRetrievalPolicy(ctx, m.policyCache.state, m.policyCache.gates, m.policyCache.metrics, parity, augmentedUsed, baselineFinal)
	m.policyCache.dirty = true
	m.policyCache.queryCount++
	if m.policyCache.flushEvery > 0 && m.policyCache.queryCount >= m.policyCache.flushEvery {
		m.flushPolicyCacheLocked(ctx)
	}
	state := m.policyCache.state
	m.retrievalPolicyMu.Unlock()

	switch state.Mode {
	case retrievalModePromoted:
		return augmentedFinal, nil
	case retrievalModeRollback, retrievalModeShadow:
		return baselineFinal, nil
	default:
		return baselineFinal, nil
	}
}

func (m *MemoryStore) postProcessMergedResults(
	ctx context.Context,
	merged []memory.SearchResult,
	opts memory.SearchOptions,
	limit int,
) []memory.SearchResult {
	if len(merged) == 0 {
		return nil
	}

	// 1. Recency decay
	halfLife := opts.HalfLifeHours
	if halfLife <= 0 {
		halfLife = m.cfg.DefaultHalfLifeHours
	}

	// Batch-fetch all recall items referenced by merged results (single query).
	recallIDs := make([]ids.UUID, 0, len(merged))
	for _, r := range merged {
		if !r.ID.IsZero() && !strings.HasPrefix(r.Source, "working-context:") && !strings.HasPrefix(r.Source, "dag:") {
			recallIDs = append(recallIDs, r.ID)
		}
	}
	recallBatch, batchErr := m.delegate.GetRecallItemsByIDs(ctx, m.agentID, recallIDs)
	if batchErr != nil {
		recallBatch = make(map[ids.UUID]*memory.RecallItem)
	}

	createdAtMap := make(map[ids.UUID]time.Time, len(recallBatch))
	for id, item := range recallBatch {
		createdAtMap[id] = item.CreatedAt
	}
	ApplyRecencyDecay(merged, time.Now(), halfLife, func(id ids.UUID) time.Time {
		return createdAtMap[id]
	})

	// 2. Metadata pre-filtering (sectors, session_key, date range)
	merged = m.applyMetadataFiltersBatch(ctx, merged, opts, recallBatch)

	// 3. Filter by min score
	if opts.MinScore > 0 {
		filtered := merged[:0]
		for _, r := range merged {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		merged = filtered
	}

	// 4. Limit
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func (m *MemoryStore) hybridProjectionSearch(ctx context.Context, query string, opts memory.SearchOptions, limit int) ([]memory.SearchResult, error) {
	if opts.SessionKey == "" || limit <= 0 {
		return nil, nil
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))
	results := make([]memory.SearchResult, 0, limit)

	// Working-context view
	wc, err := m.delegate.GetWorkingContext(ctx, m.agentID, opts.SessionKey)
	if err == nil && wc != nil && strings.TrimSpace(wc.Content) != "" &&
		queryLower != "" &&
		strings.Contains(strings.ToLower(wc.Content), queryLower) &&
		!looksLikeMemorySearchPromptEcho(wc.Content, query) {
		content := wc.Content
		if len(content) > 1200 {
			content = content[:1200] + "..."
		}
		results = append(results, memory.SearchResult{
			ID:      ids.New(),
			Content: content,
			Source:  "working-context:" + opts.SessionKey,
			Score:   0.95,
			Sector:  memory.SectorReflective,
		})
	}

	// DAG summary view
	type dagQueryProvider interface {
		Queries() *memsqlc.Queries
	}
	if provider, ok := m.delegate.(dagQueryProvider); ok && provider.Queries() != nil {
		snap, err := provider.Queries().GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
			AgentID:    m.agentID,
			SessionKey: opts.SessionKey,
		})
		if err == nil {
			nodes, err := provider.Queries().ListDAGNodesBySnapshotID(ctx, memsqlc.ListDAGNodesBySnapshotIDParams{
				SnapshotID: snap.ID,
			})
			if err == nil {
				for _, node := range nodes {
					if queryLower != "" && !strings.Contains(strings.ToLower(node.Summary), queryLower) {
						continue
					}
					if looksLikeMemorySearchPromptEcho(node.Summary, query) {
						continue
					}
					score := 0.55 + (0.1 * float64(node.Level))
					if score > 0.95 {
						score = 0.95
					}
					results = append(results, memory.SearchResult{
						ID:      ids.New(),
						Content: node.Summary,
						Source:  fmt.Sprintf("dag:%s:%s", opts.SessionKey, node.NodeID),
						Score:   score,
						Sector:  memory.SectorReflective,
					})
				}
			}
		}
	}

	if len(results) == 0 {
		return nil, nil
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// applyMetadataFiltersBatch removes results that don't match the requested sector,
// session_key, or date range constraints. Uses a pre-fetched batch of recall items
// to avoid N+1 queries.
func (m *MemoryStore) applyMetadataFiltersBatch(ctx context.Context, results []memory.SearchResult, opts memory.SearchOptions, batch map[ids.UUID]*memory.RecallItem) []memory.SearchResult {
	needSectorFilter := len(opts.Sectors) > 0
	needSessionFilter := opts.SessionKey != ""
	needDateFilter := opts.DateAfter != nil || opts.DateBefore != nil

	if !needSectorFilter && !needSessionFilter && !needDateFilter {
		return results
	}

	sectorSet := make(map[memory.Sector]bool, len(opts.Sectors))
	for _, s := range opts.Sectors {
		sectorSet[s] = true
	}

	filtered := results[:0]
	for _, r := range results {
		if strings.HasPrefix(r.Source, "working-context:") || strings.HasPrefix(r.Source, "dag:") {
			if needSessionFilter && !strings.Contains(r.Source, opts.SessionKey) {
				continue
			}
			if needSectorFilter && !sectorSet[r.Sector] {
				continue
			}
			if needDateFilter {
				continue
			}
			filtered = append(filtered, r)
			continue
		}

		item := batch[r.ID]
		if item == nil {
			continue
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
			items = filterSearchPromptEchoItems(items, query)
			if len(items) > 0 {
				return recallItemsToResults(items), nil
			}
		}
	}

	// Fall back to LIKE-based keyword search
	items, err := m.delegate.SearchRecallByKeyword(ctx, query, opts.AgentID, limit)
	if err != nil {
		return nil, err
	}
	items = filterSearchPromptEchoItems(items, query)
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

// vectorSearchGoSide performs Go-side brute-force cosine similarity search.
// Uses an in-memory cache of embeddings (populated lazily, updated on insert)
// to avoid re-scanning the DB on every search call.
func (m *MemoryStore) vectorSearchGoSide(ctx context.Context, queryVec memory.Embedding, limit int) ([]memory.SearchResult, error) {
	items, err := m.ensureVecCacheLoaded(ctx)
	if err != nil {
		return nil, err
	}
	return VectorSearch(queryVec, items, limit), nil
}

const maxVectorSearchChunks = 50_000

// ensureVecCacheLoaded populates the vector cache from DB on first access.
func (m *MemoryStore) ensureVecCacheLoaded(ctx context.Context) ([]VectorSearchInput, error) {
	m.vecCache.mu.RLock()
	if m.vecCache.loaded {
		items := m.vecCache.items
		m.vecCache.mu.RUnlock()
		return items, nil
	}
	m.vecCache.mu.RUnlock()

	m.vecCache.mu.Lock()
	defer m.vecCache.mu.Unlock()

	// Double-check after acquiring write lock
	if m.vecCache.loaded {
		return m.vecCache.items, nil
	}

	var allChunks []*memory.ArchivalChunk
	offset := 0
	batchSize := 5000
	for len(allChunks) < maxVectorSearchChunks {
		batch, err := m.delegate.ListAllArchivalChunks(ctx, m.agentID, batchSize, offset)
		if err != nil {
			return nil, err
		}
		allChunks = append(allChunks, batch...)
		if len(batch) < batchSize {
			break
		}
		offset += batchSize
	}
	if len(allChunks) > maxVectorSearchChunks {
		allChunks = allChunks[:maxVectorSearchChunks]
	}

	items := make([]VectorSearchInput, 0, len(allChunks))
	for _, chunk := range allChunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		items = append(items, VectorSearchInput{
			Chunk:     chunk,
			Embedding: chunk.Embedding,
		})
	}

	m.vecCache.items = items
	m.vecCache.loaded = true
	return items, nil
}

// appendToVecCache adds newly inserted chunks to the in-memory vector cache
// without triggering a full reload.
func (m *MemoryStore) appendToVecCache(chunks []*memory.ArchivalChunk) {
	m.vecCache.mu.Lock()
	defer m.vecCache.mu.Unlock()
	if !m.vecCache.loaded {
		return // not yet populated; first search will load everything
	}
	for _, chunk := range chunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		if len(m.vecCache.items) >= maxVectorSearchChunks {
			break
		}
		m.vecCache.items = append(m.vecCache.items, VectorSearchInput{
			Chunk:     chunk,
			Embedding: chunk.Embedding,
		})
	}
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

func filterSearchPromptEchoItems(items []*memory.RecallItem, query string) []*memory.RecallItem {
	if len(items) == 0 {
		return nil
	}

	filtered := items[:0]
	for _, item := range items {
		if shouldSuppressSearchPromptEcho(item, query) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func shouldSuppressSearchPromptEcho(item *memory.RecallItem, query string) bool {
	if item == nil {
		return false
	}
	if item.Role != "user" {
		return false
	}
	if !strings.Contains(strings.ToLower(item.Tags), "session-message") {
		return false
	}
	return looksLikeMemorySearchPromptEcho(item.Content, query)
}

func looksLikeMemorySearchPromptEcho(content, query string) bool {
	lowerContent := strings.ToLower(strings.TrimSpace(content))
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if lowerContent == "" || lowerQuery == "" {
		return false
	}
	if !strings.Contains(lowerContent, lowerQuery) {
		return false
	}

	for _, needle := range []string{
		"search your memory",
		"search my memory",
		"look in your memory",
		"look through your memory",
		"what do you remember",
		"tell me what you find",
		"tell me what you remember",
	} {
		if strings.Contains(lowerContent, needle) {
			return true
		}
	}
	return false
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

	archivalCount, err := m.delegate.CountArchivalChunks(ctx, m.agentID)
	if err != nil {
		return nil, err
	}

	recallTokens, err := m.estimateRecallTokens(ctx, agentID, sessionKey, recallCount)
	if err != nil {
		return nil, err
	}

	estimatedTotal := wcTokens + recallTokens
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
// Also flushes in-memory caches (retrieval policy) to KV.
func (m *MemoryStore) Sync() error {
	m.FlushPolicyCache(context.Background())
	type syncer interface{ Sync() error }
	if s, ok := m.delegate.(syncer); ok {
		return s.Sync()
	}
	return nil
}

func (m *MemoryStore) Close() error {
	m.FlushPolicyCache(context.Background())
	return m.delegate.Close()
}

// --- helpers ---

func (m *MemoryStore) estimateRecallTokens(ctx context.Context, agentID, sessionKey string, recallCount int) (int, error) {
	if recallCount <= 0 {
		return 0, nil
	}

	// Sample and scale for very large recall sets to avoid expensive full scans.
	const maxSampleSize = 200
	sampleSize := recallCount
	if sampleSize > maxSampleSize {
		sampleSize = maxSampleSize
	}

	items, err := m.delegate.ListRecallItems(ctx, agentID, sessionKey, sampleSize, 0)
	if err != nil {
		return 0, fmt.Errorf("list recall items for token estimate: %w", err)
	}
	if len(items) == 0 {
		return 0, nil
	}

	sampleTokens := 0
	for _, item := range items {
		sampleTokens += estimateTokens(item.Content)
	}

	if recallCount <= len(items) {
		return sampleTokens, nil
	}

	avgTokens := sampleTokens / len(items)
	return sampleTokens + avgTokens*(recallCount-len(items)), nil
}

func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return observation.EstimateTokens(s)
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
