package cortex

import (
	"context"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// ConsolidationStore is the minimal interface for memory consolidation.
// Implemented by LibSQLDelegate via sqlc-generated queries.
type ConsolidationStore interface {
	// ListRecallItemsForConsolidation returns recent recall items with their embeddings
	// for similarity comparison. Returns items created after the given cutoff time.
	ListRecallItemsForConsolidation(ctx context.Context, agentID string, cutoff time.Time, limit int) ([]*memory.RecallItem, error)

	// InsertMemoryEdge creates a relationship edge between two memory items.
	InsertMemoryEdge(ctx context.Context, edge *memory.MemoryEdge) error

	// CountMemoryEdgesForItem returns the number of edges connected to an item.
	CountMemoryEdgesForItem(ctx context.Context, memoryID ids.UUID) (int, error)
}

// SimilarityChecker computes cosine similarity between embeddings.
type SimilarityChecker interface {
	// Similarity returns cosine similarity between two vectors, range [-1, 1].
	Similarity(a, b []float32) float64
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (normA * normB)
}

// ConsolidationConfig configures the memory consolidation task.
type ConsolidationConfig struct {
	SimilarityThreshold float64       // Minimum similarity to create edge (default 0.85)
	MergeThreshold      float64       // Minimum similarity to consider merging (default 0.95)
	LookbackWindow      time.Duration // How far back to look for items (default 24h)
	BatchSize           int           // Max items to process per run (default 50)
	MaxEdgesPerItem     int           // Max edges to create per item (default 5)
	Interval            time.Duration // How often to run (default 10 minutes)
	Timeout             time.Duration // Max execution time (default 60 seconds)
}

// DefaultConsolidationConfig returns sensible defaults for memory consolidation.
func DefaultConsolidationConfig() ConsolidationConfig {
	return ConsolidationConfig{
		SimilarityThreshold: 0.85,
		MergeThreshold:      0.95,
		LookbackWindow:      24 * time.Hour,
		BatchSize:           50,
		MaxEdgesPerItem:     5,
		Interval:            10 * time.Minute,
		Timeout:             60 * time.Second,
	}
}

// ConsolidationTask finds similar memory items and creates relational edges between them.
// It runs periodically to build the memory graph without blocking the main agent loop.
type ConsolidationTask struct {
	cfg   ConsolidationConfig
	store ConsolidationStore
}

// NewConsolidationTask creates a consolidation task with the given config and store.
// If store is nil, the task becomes a no-op.
// Validates that SimilarityThreshold and MergeThreshold are between 0 and 1,
// and ensures MergeThreshold >= SimilarityThreshold.
func NewConsolidationTask(cfg ConsolidationConfig, store ConsolidationStore) *ConsolidationTask {
	// Validate SimilarityThreshold: must be between 0 and 1 (default 0.85)
	if cfg.SimilarityThreshold < 0 || cfg.SimilarityThreshold > 1 {
		cfg.SimilarityThreshold = 0.85
	}
	// Validate MergeThreshold: must be between 0 and 1 (default 0.95)
	if cfg.MergeThreshold < 0 || cfg.MergeThreshold > 1 {
		cfg.MergeThreshold = 0.95
	}
	// Ensure MergeThreshold >= SimilarityThreshold
	if cfg.MergeThreshold < cfg.SimilarityThreshold {
		cfg.MergeThreshold = cfg.SimilarityThreshold + 0.1
	}
	return &ConsolidationTask{cfg: cfg, store: store}
}

func (t *ConsolidationTask) Name() string            { return "consolidation" }
func (t *ConsolidationTask) Interval() time.Duration { return t.cfg.Interval }
func (t *ConsolidationTask) Timeout() time.Duration  { return t.cfg.Timeout }

func (t *ConsolidationTask) Execute(ctx context.Context) error {
	if t.store == nil {
		logger.DebugCF("cortex", "Consolidation task skipped: no store configured", nil)
		return nil
	}

	// For now, we process items without requiring an explicit agentID filter
	// The delegate implementations handle agent scoping internally
	cutoff := time.Now().Add(-t.cfg.LookbackWindow)

	// Fetch recent items for consolidation
	// Note: We pass empty agentID to get all items; delegate should handle this
	items, err := t.store.ListRecallItemsForConsolidation(ctx, "", cutoff, t.cfg.BatchSize)
	if err != nil {
		return err
	}

	if len(items) < 2 {
		logger.DebugCF("cortex", "Consolidation: insufficient items for comparison", nil)
		return nil
	}

	edgesCreated := 0
	// Compare each pair once (O(N^2/2) comparisons for small batches)
	for i := 0; i < len(items) && edgesCreated < t.cfg.MaxEdgesPerItem*len(items); i++ {
		itemA := items[i]
		if itemA.Embedding == nil || len(itemA.Embedding) == 0 {
			continue
		}

		edgesForItem := 0
		for j := i + 1; j < len(items) && edgesForItem < t.cfg.MaxEdgesPerItem; j++ {
			itemB := items[j]
			if itemB.Embedding == nil || len(itemB.Embedding) == 0 {
				continue
			}

			// Check if edge already exists (avoid duplicates)
			existingCount, _ := t.store.CountMemoryEdgesForItem(ctx, itemA.ID)
			if existingCount >= t.cfg.MaxEdgesPerItem {
				break
			}

			sim := cosineSimilarity(itemA.Embedding, itemB.Embedding)
			if sim >= t.cfg.SimilarityThreshold {
				edgeType := memory.EdgeRelatedTo
				if sim >= t.cfg.MergeThreshold {
					edgeType = memory.EdgeUpdates // High similarity suggests update relationship
				}

				edge := &memory.MemoryEdge{
					FromID:   itemA.ID,
					ToID:     itemB.ID,
					EdgeType: edgeType,
					Weight:   sim,
				}

				if err := t.store.InsertMemoryEdge(ctx, edge); err != nil {
					logger.WarnCF("cortex", "Failed to insert memory edge",
						map[string]interface{}{
							"from":  itemA.ID.String(),
							"to":    itemB.ID.String(),
							"error": err.Error(),
						})
					continue
				}

				edgesCreated++
				edgesForItem++
			}
		}
	}

	if edgesCreated > 0 {
		logger.DebugCF("cortex", "Consolidation task completed",
			map[string]interface{}{
				"edges_created": edgesCreated,
				"items_checked": len(items),
				"threshold":     t.cfg.SimilarityThreshold,
			})
	}

	return nil
}
