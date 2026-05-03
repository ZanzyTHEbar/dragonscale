package agent

import (
	"context"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

type TaskCompletion = memory.TaskCompletionRecord
type MemoryRating = memory.MemoryRating

// TaskCompletionStore is the interface for storing task completion records.
// Implemented by the memory delegate.
type TaskCompletionStore interface {
	StoreTaskCompletion(ctx context.Context, agentID string, completion TaskCompletion, conversationID, runID ids.UUID) error
}

// endTask stores task completion data and self-reports.
// This is called at the end of an agent run to record the outcome for RL analysis.
func (al *AgentLoop) endTask(ctx context.Context, conversationID, runID ids.UUID, completion TaskCompletion) error {
	if al.memDelegate == nil {
		return nil
	}
	ctx = context.WithoutCancel(ctx)

	// Store self-report scores if the delegate implements RLStore
	if rlStore, ok := al.memDelegate.(interface {
		UpdateMemorySelfReport(ctx context.Context, memoryID ids.UUID, score int) error
	}); ok {
		for _, rating := range completion.SelfReports {
			if err := rlStore.UpdateMemorySelfReport(ctx, rating.MemoryID, rating.Score); err != nil {
				// Log but don't fail - self-reports are best-effort
				logger.WarnCF("agent", "Failed to store self-report",
					map[string]interface{}{
						"memory_id": rating.MemoryID.String(),
						"error":     err.Error(),
					})
			}
		}
	}

	// Store task completion record if the delegate implements TaskCompletionStore
	if store, ok := al.memDelegate.(TaskCompletionStore); ok {
		if err := store.StoreTaskCompletion(ctx, pkg.NAME, completion, conversationID, runID); err != nil {
			return err
		}
	}

	return nil
}
