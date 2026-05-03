// Package delegate provides RL (Reinforcement Learning) type definitions
// that mirror the cortex package types to avoid circular imports.
package delegate

import (
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// TaskBaseline tracks running statistics for task performance using Welford's online algorithm.
// This enables incremental calculation of mean and variance without storing all historical data.
// Mirrors cortex.TaskBaseline.
type TaskBaseline struct {
	Count               int
	MeanTokens          float64
	MeanErrors          float64
	MeanUserCorrections float64
	M2Tokens            float64 // sum of squares of differences from mean (for variance)
	M2Errors            float64
	M2UserCorrections   float64
}

// TaskRecord represents a completed task with performance metrics.
// Mirrors cortex.TaskRecord.
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
// Mirrors cortex.RetrievedMemoryRecord.
type RetrievedMemoryRecord struct {
	MemoryID        ids.UUID
	Similarity      float64
	SelfReportScore *int // nullable 0-3 scale
}

// AuditEntry represents a single audit log entry for analysis.
// Mirrors cortex.AuditEntry.
type AuditEntry struct {
	ID         string
	Timestamp  time.Time
	ToolName   string
	ToolCallID string
	ToolInput  string
	Success    bool
	ErrorMsg   string
	SessionID  string
	AgentID    string
}

// DetectedPattern represents a pattern detected from audit analysis.
// Mirrors cortex.DetectedPattern.
type DetectedPattern struct {
	Type        string // "correction", "discovery", "failure_pattern"
	Description string
	Weight      float64
	Category    string
	SessionID   string
	AgentID     string
}

// SessionSummary represents token usage summary for a session.
// Mirrors cortex.SessionSummary.
type SessionSummary struct {
	SessionID   string
	AgentID     string
	TotalTokens int64
	ToolCounts  map[string]int
}
