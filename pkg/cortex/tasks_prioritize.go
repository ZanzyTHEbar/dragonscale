package cortex

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// PrioritizeStore provides access to memory for prioritization.
type PrioritizeStore interface {
	// GetUnprocessedItems retrieves memory items not yet analyzed for actionability
	GetUnprocessedItems(ctx context.Context, agentID string, since time.Time, limit int) ([]*memory.RecallItem, error)

	// MarkAsProcessed marks items as processed by the prioritizer
	MarkAsProcessed(ctx context.Context, itemIDs []ids.UUID) error

	// StoreActionableItem saves an extracted actionable item
	StoreActionableItem(ctx context.Context, item *ActionableItem) error

	// GetActionableItems retrieves current actionable items
	GetActionableItems(ctx context.Context, agentID string, status ActionableStatus) ([]*ActionableItem, error)

	// UpdateActionableStatus updates the status of an actionable item
	UpdateActionableStatus(ctx context.Context, itemID ids.UUID, status ActionableStatus) error
}

// ActionableStatus represents the state of an actionable item.
type ActionableStatus string

const (
	StatusPending    ActionableStatus = "pending"
	StatusInProgress ActionableStatus = "in_progress"
	StatusCompleted  ActionableStatus = "completed"
	StatusBlocked    ActionableStatus = "blocked"
	StatusCancelled  ActionableStatus = "cancelled"
)

// ActionableItem represents an extracted task or action.
type ActionableItem struct {
	ID          ids.UUID         `json:"id"`
	AgentID     string           `json:"agent_id"`
	SourceID    ids.UUID         `json:"source_id"` // Original memory item
	Content     string           `json:"content"`
	Status      ActionableStatus `json:"status"`
	Priority    float64          `json:"priority"` // 0.0-1.0
	MicroSteps  []string         `json:"micro_steps"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	DueAt       *time.Time       `json:"due_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`

	// Scoring components
	Urgency     float64 `json:"urgency"`     // Time sensitivity
	Importance  float64 `json:"importance"`  // Value/impact
	Feasibility float64 `json:"feasibility"` // Ease of completion
}

// PrioritizeConfig configures the prioritization task.
type PrioritizeConfig struct {
	ProcessInterval time.Duration // How often to scan for new items (default 5 min)
	MaxItemsPerRun  int           // Max items to process per run (default 50)
	LookbackWindow  time.Duration // How far back to look (default 1h)
	Timeout         time.Duration // Max execution time (default 60s)

	// Priority scoring weights
	UrgencyWeight     float64 // Weight for time sensitivity (default 0.4)
	ImportanceWeight  float64 // Weight for value/impact (default 0.4)
	FeasibilityWeight float64 // Weight for ease (default 0.2)
}

// DefaultPrioritizeConfig returns sensible defaults.
func DefaultPrioritizeConfig() PrioritizeConfig {
	return PrioritizeConfig{
		ProcessInterval:   5 * time.Minute,
		MaxItemsPerRun:    50,
		LookbackWindow:    1 * time.Hour,
		Timeout:           60 * time.Second,
		UrgencyWeight:     0.4,
		ImportanceWeight:  0.4,
		FeasibilityWeight: 0.2,
	}
}

// PrioritizeTask auto-extracts actionable items from memory and generates micro-steps.
type PrioritizeTask struct {
	cfg   PrioritizeConfig
	store PrioritizeStore
	llm   LLMClient
}

// NewPrioritizeTask creates a prioritization task.
func NewPrioritizeTask(cfg PrioritizeConfig, store PrioritizeStore, llm LLMClient) *PrioritizeTask {
	return &PrioritizeTask{
		cfg:   cfg,
		store: store,
		llm:   llm,
	}
}

// Name returns the task identifier.
func (t *PrioritizeTask) Name() string {
	return "prioritize"
}

// Interval returns the task run interval.
func (t *PrioritizeTask) Interval() time.Duration {
	return t.cfg.ProcessInterval
}

// Execute performs prioritization of memory items.
func (t *PrioritizeTask) Execute(ctx context.Context) error {
	logger.InfoCF("cortex", "Running prioritization scan", map[string]interface{}{"task": "prioritize"})

	agentID := "default" // TODO: Iterate over all agents

	since := time.Now().Add(-t.cfg.LookbackWindow)

	// Get unprocessed items
	items, err := t.store.GetUnprocessedItems(ctx, agentID, since, t.cfg.MaxItemsPerRun)
	if err != nil {
		return fmt.Errorf("get unprocessed items: %w", err)
	}

	if len(items) == 0 {
		logger.DebugCF("cortex", "No new items to prioritize", map[string]interface{}{"task": "prioritize"})
		return nil
	}

	logger.DebugCF("cortex", "Processing items for actionability", map[string]interface{}{
		"task":  "prioritize",
		"count": len(items),
	})

	// Process each item
	var processed []ids.UUID
	var extracted int

	for _, item := range items {
		actionable, isActionable := t.analyzeItem(ctx, item)
		if isActionable {
			if err := t.store.StoreActionableItem(ctx, actionable); err != nil {
				logger.DebugCF("cortex", "Failed to store actionable item", map[string]interface{}{
					"error":   err,
					"item_id": item.ID,
				})
				continue
			}
			extracted++
		}
		processed = append(processed, item.ID)
	}

	// Mark items as processed
	if err := t.store.MarkAsProcessed(ctx, processed); err != nil {
		logger.DebugCF("cortex", "Failed to mark items as processed", map[string]interface{}{"error": err})
	}

	logger.DebugCF("cortex", "Prioritization complete", map[string]interface{}{
		"task":      "prioritize",
		"processed": len(processed),
		"extracted": extracted,
	})

	return nil
}

// analyzeItem determines if a memory item contains an actionable task.
func (t *PrioritizeTask) analyzeItem(ctx context.Context, item *memory.RecallItem) (*ActionableItem, bool) {
	// Heuristic analysis for actionability
	content := item.Content

	// Check for action indicators
	actionPatterns := []string{
		"need to", "should", "must", "have to", "plan to",
		"todo", "task", "action", "follow up", "remind",
		"deadline", "due", "schedule", "book", "buy",
	}

	contentLower := strings.ToLower(content)
	actionScore := 0.0
	for _, pattern := range actionPatterns {
		if strings.Contains(contentLower, pattern) {
			actionScore += 0.2
		}
	}

	// Cap at 1.0
	if actionScore > 1.0 {
		actionScore = 1.0
	}

	// Minimum threshold for actionability
	if actionScore < 0.4 {
		return nil, false
	}

	// Calculate priority components
	urgency := t.calculateUrgency(content, item.CreatedAt)
	importance := t.calculateImportance(content, item.Importance)
	feasibility := t.calculateFeasibility(content)

	// Weighted priority score
	priority := t.cfg.UrgencyWeight*urgency +
		t.cfg.ImportanceWeight*importance +
		t.cfg.FeasibilityWeight*feasibility

	// Generate micro-steps if LLM available
	microSteps := t.generateMicroSteps(ctx, content)

	actionable := &ActionableItem{
		ID:          ids.New(),
		AgentID:     item.AgentID,
		SourceID:    item.ID,
		Content:     t.extractActionDescription(content),
		Status:      StatusPending,
		Priority:    priority,
		MicroSteps:  microSteps,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Urgency:     urgency,
		Importance:  importance,
		Feasibility: feasibility,
	}

	// Extract due date if present
	if dueAt := t.extractDueDate(content); dueAt != nil {
		actionable.DueAt = dueAt
	}

	return actionable, true
}

func (t *PrioritizeTask) calculateUrgency(content string, createdAt time.Time) float64 {
	urgency := 0.5 // Base urgency

	// Time decay: older items become more urgent
	age := time.Since(createdAt)
	if age > 24*time.Hour {
		urgency += 0.1
	}
	if age > 7*24*time.Hour {
		urgency += 0.2
	}

	// Keywords indicating urgency
	urgentWords := []string{"urgent", "asap", "immediately", "deadline", "due", "tomorrow", "today"}
	contentLower := strings.ToLower(content)
	for _, word := range urgentWords {
		if strings.Contains(contentLower, word) {
			urgency += 0.15
		}
	}

	return math.Min(urgency, 1.0)
}

func (t *PrioritizeTask) calculateImportance(content string, memoryImportance float64) float64 {
	importance := memoryImportance // Start with memory importance (0.0-1.0)

	// Keywords indicating importance
	importantWords := []string{"important", "critical", "essential", "key", "crucial", "priority"}
	contentLower := strings.ToLower(content)
	for _, word := range importantWords {
		if strings.Contains(contentLower, word) {
			importance += 0.1
		}
	}

	return math.Min(importance, 1.0)
}

func (t *PrioritizeTask) calculateFeasibility(content string) float64 {
	feasibility := 0.7 // Base feasibility

	contentLower := strings.ToLower(content)

	// Factors that reduce feasibility
	hardWords := []string{"complex", "difficult", "hard", "impossible", "expensive", "time-consuming"}
	for _, word := range hardWords {
		if strings.Contains(contentLower, word) {
			feasibility -= 0.15
		}
	}

	// Factors that increase feasibility
	easyWords := []string{"easy", "simple", "quick", "fast", "straightforward"}
	for _, word := range easyWords {
		if strings.Contains(contentLower, word) {
			feasibility += 0.1
		}
	}

	return math.Max(0.1, math.Min(feasibility, 1.0))
}

func (t *PrioritizeTask) extractActionDescription(content string) string {
	// Try to extract just the actionable part
	// Look for patterns like "I need to...", "Should...", etc.
	patterns := []string{
		"need to ",
		"should ",
		"must ",
		"have to ",
		"plan to ",
	}

	contentLower := strings.ToLower(content)
	for _, pattern := range patterns {
		if idx := strings.Index(contentLower, pattern); idx >= 0 {
			start := idx + len(pattern)
			// Take up to the next sentence or 100 chars
			end := start + 100
			if end > len(content) {
				end = len(content)
			}
			return strings.TrimSpace(content[start:end])
		}
	}

	// Return first sentence if no pattern found
	sentences := strings.Split(content, ".")
	if len(sentences) > 0 {
		return strings.TrimSpace(sentences[0])
	}

	return content
}

func (t *PrioritizeTask) generateMicroSteps(ctx context.Context, content string) []string {
	if t.llm != nil {
		prompt := fmt.Sprintf("Break down this task into 3-5 micro-steps:\n%s\n\nProvide numbered steps:", content)
		response, err := t.llm.Generate(ctx, prompt, 200)
		if err == nil && response != "" {
			return t.parseMicroSteps(response)
		}
	}

	// Fallback: generic micro-steps
	return []string{
		"Review the task requirements",
		"Gather necessary information",
		"Execute the action",
		"Verify completion",
	}
}

func (t *PrioritizeTask) parseMicroSteps(response string) []string {
	var steps []string
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Remove numbering
		if len(line) > 2 && (line[0] >= '1' && line[0] <= '9') && (line[1] == '.' || line[1] == ')') {
			line = strings.TrimSpace(line[2:])
		}
		if line != "" && !strings.HasPrefix(line, "-") {
			steps = append(steps, line)
		}
	}
	return steps
}

func (t *PrioritizeTask) extractDueDate(content string) *time.Time {
	// Simple pattern matching for dates
	// In production, use a proper NLP date parser
	contentLower := strings.ToLower(content)

	if strings.Contains(contentLower, "tomorrow") {
		tomorrow := time.Now().Add(24 * time.Hour)
		return &tomorrow
	}

	if strings.Contains(contentLower, "next week") {
		nextWeek := time.Now().Add(7 * 24 * time.Hour)
		return &nextWeek
	}

	if strings.Contains(contentLower, "this week") {
		thisWeek := time.Now().Add(3 * 24 * time.Hour)
		return &thisWeek
	}

	return nil
}

// GetTopPriorities returns the highest priority actionable items.
func GetTopPriorities(store PrioritizeStore, agentID string, n int) ([]*ActionableItem, error) {
	ctx := context.Background()
	items, err := store.GetActionableItems(ctx, agentID, StatusPending)
	if err != nil {
		return nil, err
	}

	// Sort by priority descending
	sort.Slice(items, func(i, j int) bool {
		return items[i].Priority > items[j].Priority
	})

	if len(items) > n {
		items = items[:n]
	}

	return items, nil
}
