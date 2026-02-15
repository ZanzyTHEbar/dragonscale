package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// QueueManagerConfig configures the context pressure management policy.
type QueueManagerConfig struct {
	WarnThreshold    float64 // Usage ratio to trigger warning. Default: 0.70
	OffloadThreshold float64 // Usage ratio to trigger offloading. Default: 0.80
	FlushThreshold   float64 // Usage ratio to trigger FIFO flush. Default: 0.85

	// MaxEvictBatch is the max number of recall items to evict per flush cycle.
	MaxEvictBatch int // Default: 10
}

// DefaultQueueManagerConfig returns sensible defaults.
func DefaultQueueManagerConfig() QueueManagerConfig {
	return QueueManagerConfig{
		WarnThreshold:    0.70,
		OffloadThreshold: 0.80,
		FlushThreshold:   0.85,
		MaxEvictBatch:    10,
	}
}

// QueueAction describes what the QueueManager recommends.
type QueueAction string

const (
	QueueActionNone    QueueAction = "none"    // Pressure is normal, no action needed.
	QueueActionWarn    QueueAction = "warn"    // Approaching limits, agent should be selective.
	QueueActionOffload QueueAction = "offload" // Should offload large items to archival.
	QueueActionFlush   QueueAction = "flush"   // Must evict oldest items now.
)

// QueueDecision is the output of a pressure evaluation.
type QueueDecision struct {
	Action   QueueAction
	Pressure *memory.ContextPressure
	Message  string // Human-readable explanation
}

// QueueManager monitors context pressure and makes eviction/offload decisions.
type QueueManager struct {
	store *MemoryStore
	cfg   QueueManagerConfig
}

// NewQueueManager creates a QueueManager backed by a MemoryStore.
func NewQueueManager(store *MemoryStore, cfg QueueManagerConfig) *QueueManager {
	if cfg.WarnThreshold <= 0 {
		cfg.WarnThreshold = 0.70
	}
	if cfg.OffloadThreshold <= 0 {
		cfg.OffloadThreshold = 0.80
	}
	if cfg.FlushThreshold <= 0 {
		cfg.FlushThreshold = 0.85
	}
	if cfg.MaxEvictBatch <= 0 {
		cfg.MaxEvictBatch = 10
	}
	return &QueueManager{store: store, cfg: cfg}
}

// Evaluate checks current context pressure and returns a decision.
func (q *QueueManager) Evaluate(ctx context.Context, agentID, sessionKey string) (*QueueDecision, error) {
	pressure, err := q.store.ContextUsage(ctx, agentID, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("evaluate context pressure: %w", err)
	}

	ratio := pressure.UsageRatio

	switch {
	case ratio >= q.cfg.FlushThreshold:
		return &QueueDecision{
			Action:   QueueActionFlush,
			Pressure: pressure,
			Message:  fmt.Sprintf("Context at %.0f%% capacity — FIFO flush required. Evicting oldest items.", ratio*100),
		}, nil

	case ratio >= q.cfg.OffloadThreshold:
		return &QueueDecision{
			Action:   QueueActionOffload,
			Pressure: pressure,
			Message:  fmt.Sprintf("Context at %.0f%% capacity — offloading large items to archival.", ratio*100),
		}, nil

	case ratio >= q.cfg.WarnThreshold:
		return &QueueDecision{
			Action:   QueueActionWarn,
			Pressure: pressure,
			Message:  fmt.Sprintf("Context at %.0f%% capacity — be selective with new information.", ratio*100),
		}, nil

	default:
		return &QueueDecision{
			Action:   QueueActionNone,
			Pressure: pressure,
			Message:  fmt.Sprintf("Context at %.0f%% capacity — healthy.", ratio*100),
		}, nil
	}
}

// EvictOldest performs FIFO eviction: moves the oldest recall items to archival
// and removes them from the warm tier. Returns the number of items evicted
// and a summary of what was evicted (for injection into conversation).
func (q *QueueManager) EvictOldest(ctx context.Context, agentID, sessionKey string) (int, string, error) {
	items, err := q.store.delegate.ListRecallItems(ctx, agentID, sessionKey, q.cfg.MaxEvictBatch, 0)
	if err != nil {
		return 0, "", fmt.Errorf("list oldest recall items: %w", err)
	}
	if len(items) == 0 {
		return 0, "", nil
	}

	// Oldest items are at the end (ListRecallItems returns DESC by created_at)
	// We want to evict from the tail
	evicted := 0
	var summaryParts []string

	for i := len(items) - 1; i >= 0 && evicted < q.cfg.MaxEvictBatch; i-- {
		item := items[i]

		// Archive content before removing
		_, err := q.store.StoreArchival(ctx, item.Content, "eviction:"+item.SessionKey, map[string]string{
			"agent_id":    item.AgentID,
			"session_key": item.SessionKey,
			"tags":        item.Tags + ",evicted",
			"sector":      string(item.Sector),
		})
		if err != nil {
			// Non-fatal: log and continue
			continue
		}

		// Delete from warm tier (cascade deletes archival too, but that's the old archival)
		if err := q.store.delegate.DeleteRecallItem(ctx, item.ID); err != nil {
			continue
		}

		summaryParts = append(summaryParts, truncateForSummary(item.Content))
		evicted++
	}

	summary := ""
	if evicted > 0 {
		summary = fmt.Sprintf("[Memory compaction: %d items archived]\nEvicted topics: %s",
			evicted, strings.Join(summaryParts, "; "))
	}

	return evicted, summary, nil
}

func truncateForSummary(s string) string {
	if len(s) <= 80 {
		return s
	}
	// Take first 80 chars, cut at last space
	cut := s[:80]
	if idx := strings.LastIndex(cut, " "); idx > 40 {
		cut = cut[:idx]
	}
	return cut + "..."
}
