package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/memory"
	memstore "github.com/sipeed/picoclaw/pkg/memory/store"
)

// KeywordSearchTool performs FTS5+BM25 keyword search across recall and archival memory.
type KeywordSearchTool struct {
	store   *memstore.MemoryStore
	agentID string
}

func NewKeywordSearchTool(store *memstore.MemoryStore, agentID string) *KeywordSearchTool {
	return &KeywordSearchTool{store: store, agentID: agentID}
}

func (t *KeywordSearchTool) Name() string { return "keyword_search" }

func (t *KeywordSearchTool) Description() string {
	return "Search memory using keyword matching (FTS5 with BM25 ranking). Returns matching memory entries with relevance scores. Use this for exact term matching and known phrases. Chain with chunk_read to load full content."
}

func (t *KeywordSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (keywords, phrases, or terms to match)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum results to return (default 10, max 50)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *KeywordSearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
	}

	results, err := t.store.Search(ctx, query, memory.SearchOptions{
		AgentID:       t.agentID,
		Limit:         limit,
		KeywordWeight: 1.0,
		VectorWeight:  0.0,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("keyword search failed: %v", err))
	}

	return SilentResult(formatSearchResults("keyword_search", query, results))
}

// SemanticSearchTool performs vector ANN search using embeddings.
type SemanticSearchTool struct {
	store   *memstore.MemoryStore
	agentID string
}

func NewSemanticSearchTool(store *memstore.MemoryStore, agentID string) *SemanticSearchTool {
	return &SemanticSearchTool{store: store, agentID: agentID}
}

func (t *SemanticSearchTool) Name() string { return "semantic_search" }

func (t *SemanticSearchTool) Description() string {
	return "Search memory using semantic similarity (vector embeddings). Returns entries ranked by meaning similarity, not just keyword match. Use this when looking for conceptually related content. Chain with chunk_read to load full content."
}

func (t *SemanticSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Natural language query describing what you're looking for",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum results to return (default 10, max 50)",
			},
			"threshold": map[string]interface{}{
				"type":        "number",
				"description": "Minimum similarity score (0.0-1.0, default 0.0)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SemanticSearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
	}

	minScore := 0.0
	if s, ok := args["threshold"].(float64); ok && s > 0 {
		minScore = s
	}

	results, err := t.store.Search(ctx, query, memory.SearchOptions{
		AgentID:       t.agentID,
		Limit:         limit,
		MinScore:      minScore,
		KeywordWeight: 0.0,
		VectorWeight:  1.0,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("semantic search failed: %v", err))
	}

	return SilentResult(formatSearchResults("semantic_search", query, results))
}

// ChunkReadTool loads full document/chunk content by ID.
type ChunkReadTool struct {
	store   *memstore.MemoryStore
	agentID string
}

func NewChunkReadTool(store *memstore.MemoryStore, agentID string) *ChunkReadTool {
	return &ChunkReadTool{store: store, agentID: agentID}
}

func (t *ChunkReadTool) Name() string { return "chunk_read" }

func (t *ChunkReadTool) Description() string {
	return "Load the full content of a memory entry by its ID. Use after keyword_search or semantic_search to retrieve complete content for promising results. The ID comes from search result entries."
}

func (t *ChunkReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "ID of the memory entry to read (from search results)",
			},
		},
		"required": []string{"id"},
	}
}

func (t *ChunkReadTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return ErrorResult("id is required")
	}

	content, err := t.store.ReadByID(ctx, t.agentID, id)
	if err != nil {
		return ErrorResult(fmt.Sprintf("chunk read failed: %v", err))
	}
	if content == "" {
		return ErrorResult(fmt.Sprintf("entry '%s' not found", id))
	}

	return SilentResult(content)
}

func formatSearchResults(source, query string, results []memory.SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results for '%s':\n\n", len(results), query))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s] (score: %.2f) id=%s\n", i+1, r.Source, r.Score, r.ID))

		preview := r.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("   %s\n", preview))

		if len(r.Metadata) > 0 {
			var meta []string
			for k, v := range r.Metadata {
				meta = append(meta, fmt.Sprintf("%s=%s", k, v))
			}
			sb.WriteString(fmt.Sprintf("   meta: %s\n", strings.Join(meta, ", ")))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}
