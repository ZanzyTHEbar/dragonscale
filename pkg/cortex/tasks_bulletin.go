package cortex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// BulletinStore provides access to memory for bulletin generation.
type BulletinStore interface {
	// GetRecentActivity retrieves recent memory activity
	GetRecentActivity(ctx context.Context, agentID string, since time.Time) ([]*memory.RecallItem, error)

	// GetActiveGoals retrieves current goals/focus items
	GetActiveGoals(ctx context.Context, agentID string) ([]*memory.RecallItem, error)

	// GetPendingTasks retrieves actionable items
	GetPendingTasks(ctx context.Context, agentID string) ([]*memory.RecallItem, error)

	// StoreBulletin saves the generated bulletin for injection
	StoreBulletin(ctx context.Context, agentID string, bulletin *DailyBulletin) error

	// GetLastBulletin retrieves the most recent bulletin
	GetLastBulletin(ctx context.Context, agentID string) (*DailyBulletin, error)
}

// LLMClient provides LLM generation capabilities.
type LLMClient interface {
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
}

// DailyBulletin is the cached daily briefing for system prompt injection.
type DailyBulletin struct {
	ID          ids.UUID  `json:"id"`
	AgentID     string    `json:"agent_id"`
	GeneratedAt time.Time `json:"generated_at"`
	ValidUntil  time.Time `json:"valid_until"`
	Content     string    `json:"content"`
	Tokens      int       `json:"tokens"`

	// Components for structured injection
	Summary        string   `json:"summary"`         // High-level summary
	ActiveGoals    []string `json:"active_goals"`    // Current goals
	PendingTasks   []string `json:"pending_tasks"`   // Actionable items
	KeyFacts       []string `json:"key_facts"`       // Important facts learned
	UpcomingEvents []string `json:"upcoming_events"` // Time-sensitive items
}

// BulletinConfig configures the daily bulletin generation.
type BulletinConfig struct {
	GenerateInterval time.Duration // How often to generate (default 24h)
	ValidityDuration time.Duration // How long bulletin remains valid (default 6h)
	MaxTokens        int           // Max tokens for bulletin content (default 500)
	LookbackWindow   time.Duration // How far back to look for activity (default 24h)
	Timeout          time.Duration // Max generation time (default 60s)
}

// DefaultBulletinConfig returns sensible defaults.
func DefaultBulletinConfig() BulletinConfig {
	return BulletinConfig{
		GenerateInterval: 24 * time.Hour,
		ValidityDuration: 6 * time.Hour,
		MaxTokens:        500,
		LookbackWindow:   24 * time.Hour,
		Timeout:          60 * time.Second,
	}
}

// BulletinTask generates daily LLM briefings for system prompt injection.
type BulletinTask struct {
	cfg   BulletinConfig
	store BulletinStore
	llm   LLMClient
}

// NewBulletinTask creates a bulletin generation task.
func NewBulletinTask(cfg BulletinConfig, store BulletinStore, llm LLMClient) *BulletinTask {
	return &BulletinTask{
		cfg:   cfg,
		store: store,
		llm:   llm,
	}
}

// Name returns the task identifier.
func (t *BulletinTask) Name() string {
	return "bulletin"
}

// Interval returns the task run interval.
func (t *BulletinTask) Interval() time.Duration {
	return t.cfg.GenerateInterval
}

// Execute generates the daily bulletin.
func (t *BulletinTask) Execute(ctx context.Context) error {
	logger.InfoCF("cortex", "Generating daily bulletin", map[string]interface{}{"task": "bulletin"})

	// For now, process a single agent (in production, iterate over all agents)
	agentID := "default" // TODO: Get from context or iterate

	// Check if we need to generate
	last, err := t.store.GetLastBulletin(ctx, agentID)
	if err == nil && last != nil && time.Since(last.GeneratedAt) < t.cfg.GenerateInterval {
		logger.DebugCF("cortex", "Bulletin still fresh, skipping", map[string]interface{}{"last_generated": last.GeneratedAt})
		return nil
	}

	// Gather context for bulletin generation
	bulletin, err := t.generateBulletin(ctx, agentID)
	if err != nil {
		return fmt.Errorf("generate bulletin: %w", err)
	}

	// Store the bulletin
	if err := t.store.StoreBulletin(ctx, agentID, bulletin); err != nil {
		return fmt.Errorf("store bulletin: %w", err)
	}

	logger.DebugCF("cortex", "Bulletin generated successfully", map[string]interface{}{
		"task":   "bulletin",
		"tokens": bulletin.Tokens,
	})

	return nil
}

func (t *BulletinTask) generateBulletin(ctx context.Context, agentID string) (*DailyBulletin, error) {
	since := time.Now().Add(-t.cfg.LookbackWindow)

	// Gather data
	activity, err := t.store.GetRecentActivity(ctx, agentID, since)
	if err != nil {
		return nil, fmt.Errorf("get recent activity: %w", err)
	}

	goals, err := t.store.GetActiveGoals(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get active goals: %w", err)
	}

	tasks, err := t.store.GetPendingTasks(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get pending tasks: %w", err)
	}

	// Build prompt for LLM
	prompt := t.buildBulletinPrompt(activity, goals, tasks)

	// Generate bulletin content
	content, err := t.generateContent(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("generate bulletin content: %w", err)
	}

	// Parse structured components
	bulletin := &DailyBulletin{
		ID:          ids.New(),
		AgentID:     agentID,
		GeneratedAt: time.Now(),
		ValidUntil:  time.Now().Add(t.cfg.ValidityDuration),
		Content:     content,
		Tokens:      estimateTokens(content),
	}

	// Extract components from generated content
	bulletin.Summary = t.extractSection(content, "Summary")
	bulletin.ActiveGoals = t.extractList(content, "Active Goals")
	bulletin.PendingTasks = t.extractList(content, "Pending Tasks")
	bulletin.KeyFacts = t.extractList(content, "Key Facts")
	bulletin.UpcomingEvents = t.extractList(content, "Upcoming Events")

	return bulletin, nil
}

func (t *BulletinTask) buildBulletinPrompt(activity []*memory.RecallItem, goals, tasks []*memory.RecallItem) string {
	var b strings.Builder

	b.WriteString("Generate a concise daily briefing for a personal AI assistant.\n\n")
	b.WriteString("Recent Activity (last 24h):\n")
	for _, item := range activity {
		b.WriteString(fmt.Sprintf("- %s: %s\n", item.Sector, truncate(item.Content, 100)))
	}

	b.WriteString("\nActive Goals:\n")
	for _, goal := range goals {
		b.WriteString(fmt.Sprintf("- %s\n", truncate(goal.Content, 80)))
	}

	b.WriteString("\nPending Tasks:\n")
	for _, task := range tasks {
		b.WriteString(fmt.Sprintf("- %s\n", truncate(task.Content, 80)))
	}

	b.WriteString("\nGenerate a structured briefing with these sections:\n")
	b.WriteString("1. Summary: 1-2 sentence overview of current state\n")
	b.WriteString("2. Active Goals: List current focus items\n")
	b.WriteString("3. Pending Tasks: Actionable items needing attention\n")
	b.WriteString("4. Key Facts: Important information learned recently\n")
	b.WriteString("5. Upcoming Events: Time-sensitive items\n")
	b.WriteString("\nKeep it concise and actionable. Use bullet points.")

	return b.String()
}

func (t *BulletinTask) generateContent(ctx context.Context, prompt string) (string, error) {
	if t.llm == nil {
		return "", fmt.Errorf("bulletin LLM client not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()
	content, err := t.llm.Generate(ctx, prompt, t.cfg.MaxTokens)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("bulletin generation returned empty content")
	}
	return content, nil
}

func (t *BulletinTask) extractSection(content, sectionName string) string {
	// Simple extraction: look for section header and take until next section or end
	marker := "**" + sectionName + "**"
	idx := strings.Index(content, marker)
	if idx < 0 {
		marker = sectionName + ":"
		idx = strings.Index(content, marker)
	}
	if idx < 0 {
		return ""
	}

	start := idx + len(marker)
	end := strings.Index(content[start:], "\n\n")
	if end < 0 {
		end = len(content) - start
	}

	return strings.TrimSpace(content[start : start+end])
}

func (t *BulletinTask) extractList(content, sectionName string) []string {
	section := t.extractSection(content, sectionName)
	if section == "" {
		return nil
	}

	var items []string
	lines := strings.Split(section, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			items = append(items, strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"))
		}
	}

	return items
}

func estimateTokens(s string) int {
	// Rough estimate: ~4 chars per token
	return len(s) / 4
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
