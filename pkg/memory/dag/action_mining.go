package dag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	ID         ids.UUID  `json:"id"`
	SessionID  string    `json:"session_id"`
	AgentID    string    `json:"agent_id"`
	ToolName   string    `json:"tool_name"`
	Input      string    `json:"input"`
	Output     string    `json:"output"`
	Success    bool      `json:"success"`
	DurationMs int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// ActionChain represents a sequence of related tool calls.
type ActionChain struct {
	ID            ids.UUID    `json:"id"`
	SessionID     string      `json:"session_id"`
	AgentID       string      `json:"agent_id"`
	Steps         []ChainStep `json:"steps"`
	Score         float64     `json:"score"`          // Quality score (0.0-1.0)
	ToolDiversity int         `json:"tool_diversity"` // Number of unique tools
	Success       bool        `json:"success"`
	CreatedAt     time.Time   `json:"created_at"`
	CompletedAt   time.Time   `json:"completed_at"`

	// Classification
	Intent   string `json:"intent"`   // High-level goal
	Category string `json:"category"` // Task category
}

// ChainStep represents a single step in an action chain.
type ChainStep struct {
	StepNumber  int    `json:"step_number"`
	ToolName    string `json:"tool_name"`
	Input       string `json:"input"`
	Output      string `json:"output"`
	Success     bool   `json:"success"`
	DurationMs  int    `json:"duration_ms"`
	Description string `json:"description"` // Human-readable
}

// ActionMiner mines successful tool call sequences from audit logs.
type ActionMiner struct {
	minChainLength int
	maxChainLength int
	lookbackWindow time.Duration
}

// ActionMinerConfig configures the action mining behavior.
type ActionMinerConfig struct {
	MinChainLength int           // Minimum steps to consider (default 2)
	MaxChainLength int           // Maximum steps to consider (default 10)
	LookbackWindow time.Duration // How far back to mine (default 7 days)
}

// DefaultActionMinerConfig returns sensible defaults.
func DefaultActionMinerConfig() ActionMinerConfig {
	return ActionMinerConfig{
		MinChainLength: 2,
		MaxChainLength: 10,
		LookbackWindow: 7 * 24 * time.Hour,
	}
}

// NewActionMiner creates a new action miner.
func NewActionMiner(cfg ActionMinerConfig) *ActionMiner {
	return &ActionMiner{
		minChainLength: cfg.MinChainLength,
		maxChainLength: cfg.MaxChainLength,
		lookbackWindow: cfg.LookbackWindow,
	}
}

// AuditStore provides access to audit log entries.
type AuditStore interface {
	// GetEntries retrieves audit entries within a time window
	GetEntries(ctx context.Context, agentID string, since time.Time) ([]*AuditEntry, error)

	// GetSessionEntries retrieves all entries for a specific session
	GetSessionEntries(ctx context.Context, sessionID string) ([]*AuditEntry, error)

	// StoreChain saves a mined action chain
	StoreChain(ctx context.Context, chain *ActionChain) error

	// GetTopChains retrieves the highest-scoring chains
	GetTopChains(ctx context.Context, agentID string, category string, limit int) ([]*ActionChain, error)
}

// MineChains extracts action chains from audit logs.
func (m *ActionMiner) MineChains(ctx context.Context, store AuditStore, agentID string) ([]*ActionChain, error) {
	since := time.Now().Add(-m.lookbackWindow)

	entries, err := store.GetEntries(ctx, agentID, since)
	if err != nil {
		return nil, fmt.Errorf("get audit entries: %w", err)
	}

	// Group entries by session
	sessions := groupBySession(entries)

	var chains []*ActionChain
	for sessionID, sessionEntries := range sessions {
		if len(sessionEntries) < m.minChainLength {
			continue
		}

		chain := m.buildChain(sessionID, agentID, sessionEntries)
		if chain != nil {
			chains = append(chains, chain)
		}
	}

	// Score and rank chains
	m.scoreChains(chains)

	// Sort by score descending
	sort.Slice(chains, func(i, j int) bool {
		return chains[i].Score > chains[j].Score
	})

	return chains, nil
}

func groupBySession(entries []*AuditEntry) map[string][]*AuditEntry {
	sessions := make(map[string][]*AuditEntry)
	for _, entry := range entries {
		sessions[entry.SessionID] = append(sessions[entry.SessionID], entry)
	}

	// Sort each session by time
	for _, sessionEntries := range sessions {
		sort.Slice(sessionEntries, func(i, j int) bool {
			return sessionEntries[i].CreatedAt.Before(sessionEntries[j].CreatedAt)
		})
	}

	return sessions
}

func (m *ActionMiner) buildChain(sessionID, agentID string, entries []*AuditEntry) *ActionChain {
	if len(entries) > m.maxChainLength {
		entries = entries[:m.maxChainLength]
	}

	chain := &ActionChain{
		ID:        ids.New(),
		SessionID: sessionID,
		AgentID:   agentID,
		CreatedAt: entries[0].CreatedAt,
		Steps:     make([]ChainStep, len(entries)),
	}

	uniqueTools := make(map[string]struct{})
	allSuccessful := true
	var totalDuration int

	for i, entry := range entries {
		chain.Steps[i] = ChainStep{
			StepNumber:  i + 1,
			ToolName:    entry.ToolName,
			Input:       entry.Input,
			Output:      truncate(entry.Output, 200),
			Success:     entry.Success,
			DurationMs:  entry.DurationMs,
			Description: fmt.Sprintf("Step %d: %s", i+1, describeToolCall(entry)),
		}

		uniqueTools[entry.ToolName] = struct{}{}
		if !entry.Success {
			allSuccessful = false
		}
		totalDuration += entry.DurationMs
	}

	chain.CompletedAt = entries[len(entries)-1].CreatedAt
	chain.ToolDiversity = len(uniqueTools)
	chain.Success = allSuccessful

	// Classify intent
	chain.Intent = m.classifyIntent(chain)
	chain.Category = m.classifyCategory(chain)

	return chain
}

func (m *ActionMiner) scoreChains(chains []*ActionChain) {
	for _, chain := range chains {
		chain.Score = m.calculateScore(chain)
	}
}

func (m *ActionMiner) calculateScore(chain *ActionChain) float64 {
	// Base score from success
	successScore := 0.0
	if chain.Success {
		successScore = 1.0
	} else {
		// Partial credit if most steps succeeded
		successCount := 0
		for _, step := range chain.Steps {
			if step.Success {
				successCount++
			}
		}
		successScore = float64(successCount) / float64(len(chain.Steps))
	}

	// Tool diversity bonus (more tools = more interesting)
	diversityScore := math.Min(float64(chain.ToolDiversity)/5.0, 1.0)

	// Length score (sweet spot around 3-5 steps)
	lengthScore := 1.0
	stepCount := len(chain.Steps)
	if stepCount < 2 {
		lengthScore = 0.5
	} else if stepCount > 8 {
		lengthScore = 0.8
	}

	// Recency bonus (more recent = more relevant)
	age := time.Since(chain.CreatedAt)
	recencyScore := 1.0 - math.Min(age.Hours()/(7*24), 1.0)

	// Weighted combination
	score := 0.4*successScore + 0.25*diversityScore + 0.2*lengthScore + 0.15*recencyScore

	return math.Max(0.0, math.Min(1.0, score))
}

func (m *ActionMiner) classifyIntent(chain *ActionChain) string {
	// Simple classification based on first tool
	if len(chain.Steps) == 0 {
		return "unknown"
	}

	firstTool := strings.ToLower(chain.Steps[0].ToolName)

	// Map tools to intents
	switch {
	case strings.Contains(firstTool, "search"):
		return "research"
	case strings.Contains(firstTool, "read") || strings.Contains(firstTool, "file"):
		return "read_file"
	case strings.Contains(firstTool, "write") || strings.Contains(firstTool, "edit"):
		return "write_file"
	case strings.Contains(firstTool, "run") || strings.Contains(firstTool, "exec"):
		return "execute"
	case strings.Contains(firstTool, "test"):
		return "test"
	case strings.Contains(firstTool, "git"):
		return "version_control"
	default:
		return "general"
	}
}

func (m *ActionMiner) classifyCategory(chain *ActionChain) string {
	// Classify based on tool combination patterns
	tools := make(map[string]int)
	for _, step := range chain.Steps {
		tools[strings.ToLower(step.ToolName)]++
	}

	// Check for common patterns
	hasSearch := tools["search"] > 0 || tools["grep"] > 0
	hasFileOps := tools["read_file"] > 0 || tools["write_file"] > 0 || tools["edit_file"] > 0
	hasExec := tools["run_command"] > 0 || tools["execute"] > 0
	hasGit := tools["git"] > 0

	switch {
	case hasGit:
		return "git_workflow"
	case hasSearch && hasFileOps:
		return "file_research"
	case hasFileOps && hasExec:
		return "development"
	case hasSearch && !hasFileOps:
		return "research"
	case hasExec:
		return "execution"
	default:
		return "general"
	}
}

func describeToolCall(entry *AuditEntry) string {
	// Create a human-readable description
	switch entry.ToolName {
	case "search":
		return fmt.Sprintf("Searched for '%s'", truncate(entry.Input, 40))
	case "read_file":
		return fmt.Sprintf("Read file: %s", truncate(entry.Input, 40))
	case "write_file":
		return fmt.Sprintf("Wrote to file: %s", truncate(entry.Input, 40))
	case "run_command":
		return fmt.Sprintf("Ran command: %s", truncate(entry.Input, 40))
	default:
		return fmt.Sprintf("Used %s", entry.ToolName)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// FewShotFormatter formats action chains as few-shot examples.
type FewShotFormatter struct {
	maxExamples        int
	maxStepsPerExample int
}

// NewFewShotFormatter creates a formatter for few-shot examples.
func NewFewShotFormatter(maxExamples, maxStepsPerExample int) *FewShotFormatter {
	return &FewShotFormatter{
		maxExamples:        maxExamples,
		maxStepsPerExample: maxStepsPerExample,
	}
}

// FormatChain formats a single chain as a few-shot example.
func (f *FewShotFormatter) FormatChain(chain *ActionChain) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("<example intent=\"%s\" category=\"%s\">\n", chain.Intent, chain.Category))

	for i, step := range chain.Steps {
		if i >= f.maxStepsPerExample {
			b.WriteString(fmt.Sprintf("  ... (%d more steps) ...\n", len(chain.Steps)-i))
			break
		}

		status := "✓"
		if !step.Success {
			status = "✗"
		}

		b.WriteString(fmt.Sprintf("  %s %d. %s: %s\n",
			status, step.StepNumber, step.ToolName, step.Description))
	}

	b.WriteString("</example>\n")
	return b.String()
}

// FormatExamples formats multiple chains as few-shot context.
func (f *FewShotFormatter) FormatExamples(chains []*ActionChain) string {
	if len(chains) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Successful Action Patterns\n\n")
	b.WriteString("Here are examples of successful task completion patterns:\n\n")

	for i, chain := range chains {
		if i >= f.maxExamples {
			break
		}

		b.WriteString(f.FormatChain(chain))
		b.WriteString("\n")
	}

	return b.String()
}

// FormatForPrompt formats chains for direct injection into prompts.
func (f *FewShotFormatter) FormatForPrompt(chains []*ActionChain, intent string) string {
	// Filter by intent if specified
	var filtered []*ActionChain
	for _, chain := range chains {
		if intent == "" || chain.Intent == intent || chain.Category == intent {
			filtered = append(filtered, chain)
		}
	}

	if len(filtered) == 0 {
		return ""
	}

	return f.FormatExamples(filtered)
}

// ActionReplayTask is a Cortex task for mining and caching action patterns.
type ActionReplayTask struct {
	miner  *ActionMiner
	store  AuditStore
	config ActionReplayConfig
}

// ActionReplayConfig configures the action replay task.
type ActionReplayConfig struct {
	MineInterval   time.Duration // How often to mine (default 1 hour)
	MinChainLength int           // Minimum chain length (default 2)
	TopKCache      int           // Number of chains to cache (default 20)
}

// DefaultActionReplayConfig returns sensible defaults.
func DefaultActionReplayConfig() ActionReplayConfig {
	return ActionReplayConfig{
		MineInterval:   1 * time.Hour,
		MinChainLength: 2,
		TopKCache:      20,
	}
}

// NewActionReplayTask creates an action replay Cortex task.
func NewActionReplayTask(config ActionReplayConfig, store AuditStore) *ActionReplayTask {
	return &ActionReplayTask{
		miner:  NewActionMiner(DefaultActionMinerConfig()),
		store:  store,
		config: config,
	}
}

// Name returns the task identifier.
func (t *ActionReplayTask) Name() string {
	return "action_replay"
}

// Interval returns the task run interval.
func (t *ActionReplayTask) Interval() time.Duration {
	return t.config.MineInterval
}

// Execute performs action mining and caching.
func (t *ActionReplayTask) Execute(ctx context.Context) error {
	// Mine new chains from recent sessions
	chains, err := t.miner.MineChains(ctx, t.store, "default")
	if err != nil {
		return fmt.Errorf("mine chains: %w", err)
	}

	// Store top-K chains
	for i, chain := range chains {
		if i >= t.config.TopKCache {
			break
		}
		if err := t.store.StoreChain(ctx, chain); err != nil {
			// Log but continue
			continue
		}
	}

	return nil
}

// GetFewShotContext retrieves formatted few-shot examples for prompt injection.
func GetFewShotContext(ctx context.Context, store AuditStore, agentID, intent string, maxExamples int) (string, error) {
	chains, err := store.GetTopChains(ctx, agentID, intent, maxExamples)
	if err != nil {
		return "", fmt.Errorf("get top chains: %w", err)
	}

	formatter := NewFewShotFormatter(maxExamples, 5)
	return formatter.FormatForPrompt(chains, intent), nil
}
