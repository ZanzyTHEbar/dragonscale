package cortex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// AuditAnalysisStore is the minimal interface for audit log analysis.
// Implemented by LibSQLDelegate via sqlc-generated queries.
type AuditAnalysisStore interface {
	// GetRecentAuditEntries returns audit entries since the given time.
	GetRecentAuditEntries(ctx context.Context, since time.Time) ([]AuditEntry, error)

	// StoreDetectedPattern stores a detected pattern as a recall item.
	StoreDetectedPattern(ctx context.Context, pattern DetectedPattern) error

	// GetHighTokenSessions returns sessions with token usage above threshold.
	GetHighTokenSessions(ctx context.Context, minTokens int64) ([]SessionSummary, error)

	// InsertRecallItem inserts a recall item directly (for pattern storage).
	InsertRecallItem(ctx context.Context, item *memory.RecallItem) error
}

// AuditEntry represents a single audit log entry for analysis.
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

// ToolSequence represents a tool call in a session sequence.
type ToolSequence struct {
	Tool   string
	Input  string
	Failed bool
}

// DetectedCorrection represents a correction pattern where a failed tool
// was retried with different input and succeeded.
type DetectedCorrection struct {
	FailedTool     string
	FailedInput    string
	SucceededTool  string
	SucceededInput string
	SessionID      string
}

// DetectedPattern represents a pattern detected from audit analysis.
type DetectedPattern struct {
	Type        string // "correction", "discovery", "failure_pattern"
	Description string
	Weight      float64
	Category    string
	SessionID   string
	AgentID     string
}

// SessionSummary represents token usage summary for a session.
type SessionSummary struct {
	SessionID   string
	AgentID     string
	TotalTokens int64
	ToolCounts  map[string]int
}

// AuditAnalysisConfig configures the audit analysis task.
type AuditAnalysisConfig struct {
	Interval           time.Duration // How often to run (default 10 minutes)
	Timeout            time.Duration // Max execution time (default 60 seconds)
	DiscoveryThreshold int64         // Minimum tokens for discovery (default 50000)
	FailureThreshold   int           // Minimum failures for pattern (default 3)
	LookbackWindow     time.Duration // How far back to look (default 10 minutes)
}

// DefaultAuditAnalysisConfig returns sensible defaults for audit analysis.
func DefaultAuditAnalysisConfig() AuditAnalysisConfig {
	return AuditAnalysisConfig{
		Interval:           10 * time.Minute,
		Timeout:            60 * time.Second,
		DiscoveryThreshold: 50000,
		FailureThreshold:   3,
		LookbackWindow:     10 * time.Minute,
	}
}

// AuditAnalysisTask analyzes audit logs for auto-detection patterns.
// It detects corrections, discovery sessions, and failure patterns.
type AuditAnalysisTask struct {
	cfg     AuditAnalysisConfig
	store   AuditAnalysisStore
	lastRun time.Time
}

// NewAuditAnalysisTask creates an audit analysis task with the given store.
// If store is nil, the task becomes a no-op.
func NewAuditAnalysisTask(store AuditAnalysisStore) *AuditAnalysisTask {
	return &AuditAnalysisTask{
		cfg:     DefaultAuditAnalysisConfig(),
		store:   store,
		lastRun: time.Time{}, // Zero time means check all history initially
	}
}

// Name returns the task name.
func (t *AuditAnalysisTask) Name() string { return "audit_analysis" }

// Interval returns the task interval.
func (t *AuditAnalysisTask) Interval() time.Duration { return t.cfg.Interval }

// Timeout returns the task timeout.
func (t *AuditAnalysisTask) Timeout() time.Duration { return t.cfg.Timeout }

// Execute runs the audit analysis task.
func (t *AuditAnalysisTask) Execute(ctx context.Context) error {
	if t.store == nil {
		logger.DebugCF("cortex", "Audit analysis task skipped: no store configured", nil)
		return nil
	}

	// Determine lookback window
	since := t.lastRun
	if since.IsZero() {
		since = time.Now().Add(-t.cfg.LookbackWindow)
	}

	// Get recent audit entries
	entries, err := t.store.GetRecentAuditEntries(ctx, since)
	if err != nil {
		return fmt.Errorf("failed to get recent audit entries: %w", err)
	}

	if len(entries) == 0 {
		logger.DebugCF("cortex", "Audit analysis: no new entries to process", nil)
		t.lastRun = time.Now()
		return nil
	}

	// Group entries by session
	sessions := groupBySession(entries)
	maxProcessed := since

	patternsDetected := 0

	for sessionID, sessionEntries := range sessions {
		sort.Slice(sessionEntries, func(i, j int) bool {
			return sessionEntries[i].Timestamp.Before(sessionEntries[j].Timestamp)
		})
		if n := len(sessionEntries); n > 0 && sessionEntries[n-1].Timestamp.After(maxProcessed) {
			maxProcessed = sessionEntries[n-1].Timestamp
		}

		agentID := ""
		if len(sessionEntries) > 0 {
			agentID = sessionEntries[0].AgentID
		}

		// Build tool sequence
		sequence := buildToolSequence(sessionEntries)

		// Detect corrections
		corrections := DetectCorrections(sequence)
		for _, corr := range corrections {
			pattern := DetectedPattern{
				Type:        "correction",
				Description: fmt.Sprintf("Tool %s corrected: failed with %q, succeeded with %q", corr.FailedTool, corr.FailedInput, corr.SucceededInput),
				Weight:      1.0,
				Category:    "correction",
				SessionID:   sessionID,
				AgentID:     agentID,
			}
			if err := t.storePattern(ctx, pattern); err != nil {
				logger.WarnCF("cortex", "Failed to store correction pattern",
					map[string]interface{}{"error": err.Error()})
			} else {
				patternsDetected++
			}
		}

		// Count tokens and tool usage for discovery detection
		toolCounts := countToolUsage(sessionEntries)
		totalTokens := estimateTokensFromEntries(sessionEntries)

		// Check for discovery pattern
		if IsDiscoveryWithThreshold(totalTokens, toolCounts, t.cfg.DiscoveryThreshold) {
			pattern := DetectedPattern{
				Type:        "discovery",
				Description: fmt.Sprintf("Discovery session detected: %d tokens, high read/search ratio", totalTokens),
				Weight:      1.2,
				Category:    "discovery",
				SessionID:   sessionID,
				AgentID:     agentID,
			}
			if err := t.storePattern(ctx, pattern); err != nil {
				logger.WarnCF("cortex", "Failed to store discovery pattern",
					map[string]interface{}{"error": err.Error()})
			} else {
				patternsDetected++
			}
		}

		// Detect failure patterns
		failures := filterFailures(sessionEntries)
		failurePatterns := DetectFailurePatternsWithThreshold(failures, t.cfg.FailureThreshold)
		for _, fp := range failurePatterns {
			fp.SessionID = sessionID
			fp.AgentID = agentID
			if err := t.storePattern(ctx, fp); err != nil {
				logger.WarnCF("cortex", "Failed to store failure pattern",
					map[string]interface{}{"error": err.Error()})
			} else {
				patternsDetected++
			}
		}
	}

	if patternsDetected > 0 {
		logger.DebugCF("cortex", "Audit analysis task completed",
			map[string]interface{}{
				"patterns_detected": patternsDetected,
				"entries_analyzed":  len(entries),
				"sessions_checked":  len(sessions),
			})
	}

	if maxProcessed.IsZero() {
		t.lastRun = time.Now()
	} else {
		t.lastRun = maxProcessed
	}
	return nil
}

// storePattern stores a detected pattern as a recall item.
func (t *AuditAnalysisTask) storePattern(ctx context.Context, pattern DetectedPattern) error {
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    pattern.AgentID,
		SessionKey: pattern.SessionID,
		Role:       "system",
		Sector:     memory.SectorReflective,
		Importance: pattern.Weight,
		Salience:   pattern.Weight,
		Content:    pattern.Description,
		Tags:       fmt.Sprintf("audit,%s,%s", pattern.Type, pattern.Category),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	return t.store.InsertRecallItem(ctx, item)
}

// DetectCorrections analyzes a tool sequence to find correction patterns.
// A correction is when a failed tool is retried with different input and succeeds.
func DetectCorrections(sequence []ToolSequence) []DetectedCorrection {
	var corrections []DetectedCorrection

	for i, seq := range sequence {
		if !seq.Failed {
			continue
		}

		// Look at next 3 tools
		end := i + 4
		if end > len(sequence) {
			end = len(sequence)
		}

		for j := i + 1; j < end; j++ {
			next := sequence[j]

			// Check if same tool succeeds with different input
			if next.Tool == seq.Tool && !next.Failed && next.Input != seq.Input {
				corrections = append(corrections, DetectedCorrection{
					FailedTool:     seq.Tool,
					FailedInput:    seq.Input,
					SucceededTool:  next.Tool,
					SucceededInput: next.Input,
				})
				break // Only record first correction for this failure
			}
		}
	}

	return corrections
}

// IsDiscovery determines if a session represents a discovery pattern.
// Discovery sessions have high token usage and are read/search heavy.
func IsDiscovery(tokens int64, toolCounts map[string]int) bool {
	return IsDiscoveryWithThreshold(tokens, toolCounts, 50000)
}

// IsDiscoveryWithThreshold determines if a session represents a discovery pattern
// using a configurable minimum token threshold.
func IsDiscoveryWithThreshold(tokens int64, toolCounts map[string]int, minTokens int64) bool {
	// Minimum token threshold
	if tokens < minTokens {
		return false
	}

	// Count reads and searches
	readsAndSearches := 0
	totalTools := 0

	for tool, count := range toolCounts {
		totalTools += count
		lowerTool := strings.ToLower(tool)
		if strings.Contains(lowerTool, "read") ||
			strings.Contains(lowerTool, "search") ||
			strings.Contains(lowerTool, "grep") ||
			strings.Contains(lowerTool, "find") ||
			strings.Contains(lowerTool, "list") {
			readsAndSearches += count
		}
	}

	if totalTools == 0 {
		return false
	}

	// Check if reads + searches > 50% of total
	ratio := float64(readsAndSearches) / float64(totalTools)
	return ratio > 0.5
}

// DetectFailurePatterns groups failures by tool and detects recurring patterns.
func DetectFailurePatterns(failures []AuditEntry) []DetectedPattern {
	return DetectFailurePatternsWithThreshold(failures, 3)
}

// DetectFailurePatternsWithThreshold groups failures by tool and emits patterns
// when a tool reaches the configured minimum failure count.
func DetectFailurePatternsWithThreshold(failures []AuditEntry, minFailures int) []DetectedPattern {
	if minFailures < 1 {
		minFailures = 1
	}

	// Group by tool name
	toolFailures := make(map[string][]AuditEntry)
	for _, f := range failures {
		toolFailures[f.ToolName] = append(toolFailures[f.ToolName], f)
	}

	var patterns []DetectedPattern

	for tool, toolFails := range toolFailures {
		if len(toolFails) >= minFailures {
			// Create pattern for recurring failures
			pattern := DetectedPattern{
				Type:        "failure_pattern",
				Description: fmt.Sprintf("Tool %s failed %d times: potential reliability issue", tool, len(toolFails)),
				Weight:      1.5,
				Category:    "correction",
			}
			patterns = append(patterns, pattern)
		}
	}

	return patterns
}

// groupBySession groups audit entries by session ID.
func groupBySession(entries []AuditEntry) map[string][]AuditEntry {
	groups := make(map[string][]AuditEntry)
	for _, e := range entries {
		groups[e.SessionID] = append(groups[e.SessionID], e)
	}
	return groups
}

// buildToolSequence creates a tool sequence from audit entries.
func buildToolSequence(entries []AuditEntry) []ToolSequence {
	sequence := make([]ToolSequence, 0, len(entries))
	for _, e := range entries {
		sequence = append(sequence, ToolSequence{
			Tool:   e.ToolName,
			Input:  e.ToolInput,
			Failed: !e.Success,
		})
	}
	return sequence
}

// countToolUsage counts tool usage from audit entries.
func countToolUsage(entries []AuditEntry) map[string]int {
	counts := make(map[string]int)
	for _, e := range entries {
		counts[e.ToolName]++
	}
	return counts
}

// estimateTokensFromEntries estimates total tokens from audit entries.
// Uses input length as a proxy when actual tokens aren't available.
func estimateTokensFromEntries(entries []AuditEntry) int64 {
	var total int64
	for _, e := range entries {
		// Estimate based on input length (rough approximation)
		total += int64(len(e.ToolInput)) / 4
	}
	return total
}

// filterFailures returns only failed audit entries.
func filterFailures(entries []AuditEntry) []AuditEntry {
	var failures []AuditEntry
	for _, e := range entries {
		if !e.Success {
			failures = append(failures, e)
		}
	}
	return failures
}
