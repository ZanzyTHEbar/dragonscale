package dag

import (
	"strings"
)

// ToolLoopMode controls whether the agent uses the sequential ReAct loop,
// the parallel DAG executor, or lets the router decide automatically.
type ToolLoopMode int

const (
	// ModeReAct uses Fantasy's sequential ReAct loop. Best for simple, 1-3
	// tool queries where DAG overhead isn't justified.
	ModeReAct ToolLoopMode = iota

	// ModeDAG uses the LLMCompiler-style DAG executor for parallel dispatch.
	// Best for complex multi-tool workflows.
	ModeDAG

	// ModeAuto lets the router classify the query and pick the best mode.
	ModeAuto
)

// String returns a human-readable name for the mode.
func (m ToolLoopMode) String() string {
	switch m {
	case ModeReAct:
		return "react"
	case ModeDAG:
		return "dag"
	case ModeAuto:
		return "auto"
	default:
		return "unknown"
	}
}

// RouterConfig configures the automatic mode selection heuristic.
type RouterConfig struct {
	// ComplexityThreshold is the minimum word count in a query for DAG mode.
	// Queries shorter than this default to ReAct. Default: 30.
	ComplexityThreshold int

	// ParallelKeywords are phrases that suggest parallel execution. If any
	// appear in the query, DAG mode is preferred.
	ParallelKeywords []string
}

// DefaultRouterConfig returns sensible defaults.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		ComplexityThreshold: 30,
		ParallelKeywords: []string{
			"and then",
			"in parallel",
			"simultaneously",
			"at the same time",
			"multiple",
			"several",
			"both",
			"compare",
			"aggregate",
			"combine",
			"collect",
			"gather",
		},
	}
}

// Route selects the execution mode for a query. If mode is ModeAuto, it
// applies heuristics to classify the query. Otherwise, it returns mode unchanged.
func Route(mode ToolLoopMode, query string, cfg RouterConfig) ToolLoopMode {
	if mode != ModeAuto {
		return mode
	}

	return classifyQuery(query, cfg)
}

// classifyQuery applies heuristics to determine whether a query is better
// served by the sequential ReAct loop or the parallel DAG executor.
func classifyQuery(query string, cfg RouterConfig) ToolLoopMode {
	lower := strings.ToLower(query)
	words := strings.Fields(lower)

	if len(words) >= cfg.ComplexityThreshold {
		return ModeDAG
	}

	for _, kw := range cfg.ParallelKeywords {
		if strings.Contains(lower, kw) {
			return ModeDAG
		}
	}

	// Count distinct tool-like references (heuristic for multi-tool queries).
	toolSignals := 0
	toolIndicators := []string{"search", "read", "write", "execute", "fetch", "list", "create", "delete", "find"}
	for _, ind := range toolIndicators {
		if strings.Contains(lower, ind) {
			toolSignals++
		}
	}
	if toolSignals >= 3 {
		return ModeDAG
	}

	return ModeReAct
}
