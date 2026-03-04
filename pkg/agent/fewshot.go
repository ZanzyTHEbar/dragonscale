package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
)

// FewShotProvider provides few-shot examples for prompt injection.
type FewShotProvider struct {
	store       dag.AuditStore
	formatter   *dag.FewShotFormatter
	maxExamples int
}

// NewFewShotProvider creates a new few-shot provider.
func NewFewShotProvider(store dag.AuditStore, maxExamples int) *FewShotProvider {
	return &FewShotProvider{
		store:       store,
		formatter:   dag.NewFewShotFormatter(maxExamples, 5),
		maxExamples: maxExamples,
	}
}

// GetExamples retrieves and formats few-shot examples for a given intent.
func (fsp *FewShotProvider) GetExamples(ctx context.Context, agentID, intent string) string {
	chains, err := fsp.store.GetTopChains(ctx, agentID, intent, fsp.maxExamples)
	if err != nil {
		logger.DebugCF("agent", "Failed to get few-shot chains", map[string]interface{}{"error": err})
		return ""
	}

	if len(chains) == 0 {
		return ""
	}

	return fsp.formatter.FormatForPrompt(chains, intent)
}

// GetExamplesForQuery retrieves examples matching a natural language query.
func (fsp *FewShotProvider) GetExamplesForQuery(ctx context.Context, agentID, query string) string {
	// Simple intent detection from query
	intent := detectIntent(query)
	return fsp.GetExamples(ctx, agentID, intent)
}

// detectIntent performs simple keyword-based intent detection.
func detectIntent(query string) string {
	queryLower := strings.ToLower(query)

	// Search-related
	if strings.Contains(queryLower, "find") ||
		strings.Contains(queryLower, "search") ||
		strings.Contains(queryLower, "lookup") {
		return "research"
	}

	// File operations
	if strings.Contains(queryLower, "file") ||
		strings.Contains(queryLower, "read") ||
		strings.Contains(queryLower, "write") ||
		strings.Contains(queryLower, "edit") {
		return "file_research"
	}

	// Code/development
	if strings.Contains(queryLower, "code") ||
		strings.Contains(queryLower, "function") ||
		strings.Contains(queryLower, "test") ||
		strings.Contains(queryLower, "bug") ||
		strings.Contains(queryLower, "fix") {
		return "development"
	}

	// Git operations
	if strings.Contains(queryLower, "git") ||
		strings.Contains(queryLower, "commit") ||
		strings.Contains(queryLower, "branch") ||
		strings.Contains(queryLower, "push") ||
		strings.Contains(queryLower, "pull") {
		return "git_workflow"
	}

	// Execution
	if strings.Contains(queryLower, "run") ||
		strings.Contains(queryLower, "execute") ||
		strings.Contains(queryLower, "command") ||
		strings.Contains(queryLower, "shell") {
		return "execution"
	}

	return ""
}

// BuildFewShotBlock creates a formatted few-shot block for prompt injection.
func BuildFewShotBlock(examples string) string {
	if examples == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Example Task Completion Patterns\n\n")
	b.WriteString("When approaching similar tasks, consider these successful patterns:\n\n")
	b.WriteString(examples)
	b.WriteString("\nUse these patterns as guidance when completing your current task.\n")

	return b.String()
}

// FewShotInjector handles injection of few-shot examples into prompts.
type FewShotInjector struct {
	provider *FewShotProvider
	enabled  bool
}

// NewFewShotInjector creates a few-shot injector.
func NewFewShotInjector(store dag.AuditStore, enabled bool, maxExamples int) *FewShotInjector {
	return &FewShotInjector{
		provider: NewFewShotProvider(store, maxExamples),
		enabled:  enabled,
	}
}

// Inject adds few-shot examples to a system prompt based on the user's query.
func (fsi *FewShotInjector) Inject(ctx context.Context, agentID, query, systemPrompt string) string {
	if !fsi.enabled {
		return systemPrompt
	}
	examples := fsi.provider.GetExamplesForQuery(ctx, agentID, query)
	return fsi.injectBlock(systemPrompt, examples)
}

func (fsi *FewShotInjector) injectBlock(systemPrompt, examples string) string {
	if examples == "" {
		return systemPrompt
	}
	block := BuildFewShotBlock(examples)
	if idx := strings.LastIndex(systemPrompt, "## Current Task"); idx > 0 {
		return systemPrompt[:idx] + block + "\n\n" + systemPrompt[idx:]
	}
	return systemPrompt + "\n\n" + block
}

// InjectByIntent adds few-shot examples for a specific intent category.
func (fsi *FewShotInjector) InjectByIntent(ctx context.Context, agentID, intent, systemPrompt string) string {
	if !fsi.enabled {
		return systemPrompt
	}
	examples := fsi.provider.GetExamples(ctx, agentID, intent)
	return fsi.injectBlock(systemPrompt, examples)
}

// InjectForTools adds examples showing successful usage of specific tools.
func (fsi *FewShotInjector) InjectForTools(ctx context.Context, agentID string, toolNames []string, systemPrompt string) string {
	if !fsi.enabled || len(toolNames) == 0 {
		return systemPrompt
	}
	examples := fsi.provider.GetExamples(ctx, agentID, "development")
	return fsi.injectToolBlock(systemPrompt, toolNames, examples)
}

func (fsi *FewShotInjector) injectToolBlock(systemPrompt string, toolNames []string, examples string) string {
	if examples == "" {
		return systemPrompt
	}
	var b strings.Builder
	b.WriteString("## Tool Usage Patterns\n\n")
	b.WriteString(fmt.Sprintf("When using %s, follow these patterns:\n\n", strings.Join(toolNames, ", ")))
	b.WriteString(examples)
	return systemPrompt + "\n\n" + b.String()
}

// ChainSummary provides a summary of available action chains.
type ChainSummary struct {
	TotalChains  int
	ByIntent     map[string]int
	ByCategory   map[string]int
	AverageScore float64
	TopIntents   []string
}

// GetSummary returns a summary of available chains for an agent.
func (fsp *FewShotProvider) GetSummary(ctx context.Context, agentID string) (*ChainSummary, error) {
	// Get chains across all intents
	chains, err := fsp.store.GetTopChains(ctx, agentID, "", 100)
	if err != nil {
		return nil, err
	}

	summary := &ChainSummary{
		TotalChains: len(chains),
		ByIntent:    make(map[string]int),
		ByCategory:  make(map[string]int),
	}

	var totalScore float64
	intentScores := make(map[string]float64)

	for _, chain := range chains {
		summary.ByIntent[chain.Intent]++
		summary.ByCategory[chain.Category]++
		totalScore += chain.Score
		intentScores[chain.Intent] += chain.Score
	}

	if len(chains) > 0 {
		summary.AverageScore = totalScore / float64(len(chains))
	}

	// Find top intents by count
	type intentCount struct {
		intent string
		count  int
		score  float64
	}

	var intents []intentCount
	for intent, count := range summary.ByIntent {
		intents = append(intents, intentCount{intent, count, intentScores[intent]})
	}

	sort.Slice(intents, func(i, j int) bool {
		return intents[i].count > intents[j].count
	})

	for _, ic := range intents {
		summary.TopIntents = append(summary.TopIntents, ic.intent)
	}

	return summary, nil
}
