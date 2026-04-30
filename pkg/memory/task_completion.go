package memory

import (
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// MemoryRating represents a self-reported usefulness score for a memory.
type MemoryRating struct {
	MemoryID ids.UUID
	Score    int // 0-3 scale
}

// TaskCompletionRecord tracks the outcome of an agent run for RL analysis.
type TaskCompletionRecord struct {
	TaskID          string
	Description     string
	TokensUsed      int
	ToolCalls       int
	Errors          int
	UserCorrections int
	Completed       bool
	SelfReports     []MemoryRating
	CreatedAt       time.Time
}
