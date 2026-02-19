package rlm

import (
	"strings"
)

// OpType identifies a decomposition operation the strategy planner can choose.
type OpType string

const (
	OpPeek      OpType = "peek"
	OpGrep      OpType = "grep"
	OpPartition OpType = "partition"
	OpRecurse   OpType = "recurse"
	OpFinal     OpType = "final" // terminal: no further decomposition needed
)

// StrategyOp is one step produced by the strategy planner.
type StrategyOp struct {
	Type       OpType
	PartitionK int    // for OpPartition
	GrepQuery  string // for OpGrep
	PeekStart  uint64 // for OpPeek
	PeekLength uint32 // for OpPeek
}

// StrategyConfig controls the heuristic strategy planner.
type StrategyConfig struct {
	// DirectThreshold is the context size (bytes) below which the engine
	// answers directly without decomposition. Default: 8192 (≈6K tokens).
	DirectThreshold int

	// DefaultPartitionK is the number of partitions when OpPartition is chosen.
	DefaultPartitionK int

	// MaxDepth is the maximum recursion depth. The strategy planner always
	// emits OpFinal at depth >= MaxDepth to prevent infinite recursion.
	MaxDepth int
}

// DefaultStrategyConfig returns sensible defaults.
func DefaultStrategyConfig() StrategyConfig {
	return StrategyConfig{
		DirectThreshold:   8192,
		DefaultPartitionK: 4,
		MaxDepth:          8,
	}
}

// StrategyPlanner selects the next decomposition operation given the current
// context size, query, and recursion depth. This is the "cheap sub-LM" from
// the RLM paper — it uses simple heuristics rather than an LLM call to keep
// latency and cost minimal.
//
// More sophisticated implementations could replace PlanNext with an LLM-backed
// planner, but the heuristic version already achieves the RLM paper's results
// at much lower cost.
type StrategyPlanner struct {
	cfg StrategyConfig
}

// NewStrategyPlanner creates a planner with the given config.
func NewStrategyPlanner(cfg StrategyConfig) *StrategyPlanner {
	return &StrategyPlanner{cfg: cfg}
}

// PlanNext returns the next operation to apply to the context given the query
// and current recursion depth.
func (sp *StrategyPlanner) PlanNext(contextBytes int, query string, depth uint8) StrategyOp {
	// Terminal conditions: context small enough or depth limit reached.
	if depth >= uint8(sp.cfg.MaxDepth) || contextBytes <= sp.cfg.DirectThreshold {
		return StrategyOp{Type: OpFinal}
	}

	// If the query contains specific keywords, prefer grep to narrow context.
	if looksLikeKeywordQuery(query) {
		return StrategyOp{Type: OpGrep, GrepQuery: extractKeyword(query)}
	}

	// Default: partition and recurse.
	k := sp.cfg.DefaultPartitionK
	if contextBytes > 4*1024*1024 { // > 4MB: use more partitions
		k = 8
	}
	return StrategyOp{Type: OpPartition, PartitionK: k}
}

// looksLikeKeywordQuery returns true when the query contains explicit search
// cues: quoted strings, identifiers starting with #/@, or error messages.
func looksLikeKeywordQuery(query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(q, "\"") ||
		strings.Contains(q, "error:") ||
		strings.Contains(q, "func ") ||
		strings.Contains(q, "def ") ||
		strings.HasPrefix(q, "find ") ||
		strings.HasPrefix(q, "search ") ||
		strings.HasPrefix(q, "where is ")
}

// extractKeyword pulls the most likely search term from the query.
// Falls back to the first word.
func extractKeyword(query string) string {
	// Extract first quoted string.
	if start := strings.Index(query, "\""); start >= 0 {
		if end := strings.Index(query[start+1:], "\""); end >= 0 {
			return query[start+1 : start+1+end]
		}
	}
	// First word of the query.
	words := strings.Fields(query)
	if len(words) > 0 {
		return words[0]
	}
	return query
}
