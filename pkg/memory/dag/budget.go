package dag

// BudgetConfig defines the percentage allocation for each context section.
// All percentages should sum to 100.
type BudgetConfig struct {
	SystemPromptPct int // % for system prompt (identity, rules, skills)
	ObservationsPct int // % for observation block
	KnowledgePct    int // % for knowledge block (Focus completions)
	DAGSummariesPct int // % for DAG compressed history
	RawTailPct      int // % for raw recent messages (uncompressed tail)
	ToolResultsPct  int // % for tool call results
}

// DefaultBudgetConfig returns a balanced allocation.
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		SystemPromptPct: 20,
		ObservationsPct: 10,
		KnowledgePct:    5,
		DAGSummariesPct: 25,
		RawTailPct:      30,
		ToolResultsPct:  10,
	}
}

// Budget represents concrete token allocations computed from config and context window.
type Budget struct {
	Total        int
	SystemPrompt int
	Observations int
	Knowledge    int
	DAGSummaries int
	RawTail      int
	ToolResults  int
}

// ComputeBudget calculates token allocations from a context window size and config.
func ComputeBudget(contextWindow int, cfg BudgetConfig) Budget {
	return Budget{
		Total:        contextWindow,
		SystemPrompt: contextWindow * cfg.SystemPromptPct / 100,
		Observations: contextWindow * cfg.ObservationsPct / 100,
		Knowledge:    contextWindow * cfg.KnowledgePct / 100,
		DAGSummaries: contextWindow * cfg.DAGSummariesPct / 100,
		RawTail:      contextWindow * cfg.RawTailPct / 100,
		ToolResults:  contextWindow * cfg.ToolResultsPct / 100,
	}
}

// Remaining returns tokens available after accounting for used amounts.
func (b Budget) Remaining(usedSystem, usedObs, usedKnowledge, usedDAG, usedTail, usedTools int) int {
	used := usedSystem + usedObs + usedKnowledge + usedDAG + usedTail + usedTools
	remaining := b.Total - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// SelectDAGLevel determines which DAG compression level to use given
// the available token budget for DAG summaries.
func SelectDAGLevel(d *DAG, budgetTokens int) Level {
	if d == nil || len(d.Nodes) == 0 {
		return LevelRaw
	}

	sessionTokens := d.TotalTokens(LevelSession)
	if sessionTokens > 0 && sessionTokens <= budgetTokens {
		sectionTokens := d.TotalTokens(LevelSection)
		if sectionTokens > 0 && sectionTokens <= budgetTokens {
			chunkTokens := d.TotalTokens(LevelChunk)
			if chunkTokens <= budgetTokens {
				return LevelChunk
			}
			return LevelSection
		}
		return LevelSession
	}

	return LevelSession
}

// RenderDAGForBudget renders DAG nodes at the most detailed level
// that fits within the given token budget.
func RenderDAGForBudget(d *DAG, budgetTokens int) string {
	if d == nil || len(d.Nodes) == 0 {
		return ""
	}

	level := SelectDAGLevel(d, budgetTokens)
	return d.FormatLevel(level)
}

// TailMessageCount estimates how many raw messages fit in the tail budget.
// Uses a rough average of ~50 tokens per message.
func TailMessageCount(tailBudget int) int {
	const (
		avgTokensPerMessage = 50
		minTail             = 4
	)
	if tailBudget <= 0 {
		return minTail
	}
	count := tailBudget / avgTokensPerMessage
	if count < minTail {
		return minTail
	}
	return count
}
