package cortex

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

// RLStore is the minimal interface for reinforcement learning weight updates.
// Implemented by the memory delegate via hand-written SQL.
type RLStore interface {
	GetCompletedTasks(ctx context.Context, since time.Time) ([]TaskRecord, error)
	GetRetrievedMemories(ctx context.Context, taskID string) ([]RetrievedMemoryRecord, error)
	GetTaskBaseline(ctx context.Context, agentID string) (*TaskBaseline, error)
	UpdateTaskBaseline(ctx context.Context, agentID string, baseline *TaskBaseline) error
	UpdateMemoryWeight(ctx context.Context, memoryID ids.UUID, weight, credit float64) error
	UpdateMemorySelfReport(ctx context.Context, memoryID ids.UUID, score int) error
}

// TaskRecord represents a completed task with performance metrics.
type TaskRecord struct {
	ID              string
	Description     string
	TokensUsed      int
	ToolCalls       int
	Errors          int
	UserCorrections int
	Completed       bool
	CreatedAt       time.Time
}

// RetrievedMemoryRecord represents a memory retrieved during task execution.
type RetrievedMemoryRecord struct {
	MemoryID        ids.UUID
	Similarity      float64
	SelfReportScore *int // nullable 0-3 scale
}

// RLTask applies reinforcement learning updates to memory weights based on
// task outcomes and self-reported utility scores.
type RLTask struct {
	store        RLStore
	agentID      string
	learningRate float64
	mu           sync.Mutex
	lastRun      time.Time
}

// NewRLTask creates an RL task with the given store and agent ID.
// Uses default learning rate of 0.1 if not specified.
func NewRLTask(store RLStore, agentID string) *RLTask {
	return &RLTask{
		store:        store,
		agentID:      agentID,
		learningRate: 0.1,
		lastRun:      time.Time{},
	}
}

// Name returns the task identifier.
func (t *RLTask) Name() string { return "rl" }

// Interval returns how often the task should run (5 minutes).
func (t *RLTask) Interval() time.Duration { return 5 * time.Minute }

// Timeout returns the maximum execution time for the task.
func (t *RLTask) Timeout() time.Duration { return 30 * time.Second }

// Execute runs the reinforcement learning weight update cycle.
func (t *RLTask) Execute(ctx context.Context) error {
	if t.store == nil {
		logger.DebugCF("cortex", "RL task skipped: no store configured", nil)
		return nil
	}

	// Get baseline for computing task scores
	baseline, err := t.store.GetTaskBaseline(ctx, t.agentID)
	if err != nil {
		return fmt.Errorf("failed to get task baseline: %w", err)
	}

	// Get tasks completed since last run
	tasks, err := t.store.GetCompletedTasks(ctx, t.lastRun)
	if err != nil {
		return fmt.Errorf("failed to get completed tasks: %w", err)
	}

	if len(tasks) == 0 {
		logger.DebugCF("cortex", "RL task: no completed tasks since last run", nil)
		t.updateLastRun()
		return nil
	}

	totalMemoriesUpdated := 0
	totalMemoriesWithSelfReport := 0

	// Process each completed task
	for _, task := range tasks {
		if err := t.processTask(ctx, task, baseline); err != nil {
			logger.WarnCF("cortex", "Failed to process task for RL",
				map[string]interface{}{
					"task_id": task.ID,
					"error":   err.Error(),
				})
			// Continue with other tasks - don't let one failure stop the batch
			continue
		}

		// Update baseline with task metrics
		baseline = UpdateBaseline(baseline, task.TokensUsed, task.Errors, task.UserCorrections)

		// Get memory stats for logging
		memories, err := t.store.GetRetrievedMemories(ctx, task.ID)
		if err == nil {
			totalMemoriesUpdated += len(memories)
			for _, m := range memories {
				if m.SelfReportScore != nil {
					totalMemoriesWithSelfReport++
				}
			}
		}
	}

	// Save updated baseline
	if err := t.store.UpdateTaskBaseline(ctx, t.agentID, baseline); err != nil {
		return fmt.Errorf("failed to update task baseline: %w", err)
	}

	// Update last run timestamp
	t.updateLastRun()

	logger.DebugCF("cortex", "RL task completed",
		map[string]interface{}{
			"tasks_processed":       len(tasks),
			"memories_updated":      totalMemoriesUpdated,
			"memories_with_reports": totalMemoriesWithSelfReport,
			"baseline_count":        baseline.Count,
		})

	return nil
}

// processTask handles RL updates for a single task.
func (t *RLTask) processTask(ctx context.Context, task TaskRecord, baseline *TaskBaseline) error {
	// Compute task score using baseline
	taskScore := ComputeTaskScore(baseline, task.TokensUsed, task.Errors, task.UserCorrections, task.Completed)

	// Get memories retrieved during this task
	memories, err := t.store.GetRetrievedMemories(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("failed to get retrieved memories: %w", err)
	}

	if len(memories) == 0 {
		return nil // No memories to update
	}

	numMemories := len(memories)

	// Process each retrieved memory
	for _, memory := range memories {
		selfReportScore := 0
		if memory.SelfReportScore != nil {
			selfReportScore = *memory.SelfReportScore
		}

		// Compute credit for this memory
		credit := ComputeCredit(taskScore, selfReportScore, numMemories)

		// Update memory weight using EMA
		// Note: We use 1.0 as default oldWeight since we don't store per-memory weights yet
		newWeight := UpdateWeight(1.0, credit, t.learningRate)

		// Apply weight update
		if err := t.store.UpdateMemoryWeight(ctx, memory.MemoryID, newWeight, credit); err != nil {
			logger.WarnCF("cortex", "Failed to update memory weight",
				map[string]interface{}{
					"memory_id": memory.MemoryID.String(),
					"error":     err.Error(),
				})
			// Continue with other memories
			continue
		}
	}

	return nil
}

// updateLastRun safely updates the last run timestamp.
func (t *RLTask) updateLastRun() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRun = time.Now()
}
