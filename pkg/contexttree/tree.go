// Package contexttree implements a hierarchical context management system with
// scoring, Boltzmann sampling for pruning, and hysteresis to prevent flicker.
package contexttree

import (
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// NodeType classifies the type of context node.
type NodeType string

const (
	NodeTypeRoot        NodeType = "root"
	NodeTypeMessage     NodeType = "message"
	NodeTypeToolCall    NodeType = "tool_call"
	NodeTypeSummary     NodeType = "summary"
	NodeTypeObservation NodeType = "observation"
)

// TypePriorities defines the base prior weight for each node type.
// These values are used in the scoring function to bias toward certain node types.
var TypePriorities = map[NodeType]float64{
	NodeTypeRoot:        1.0,
	NodeTypeMessage:     0.9,
	NodeTypeToolCall:    0.85,
	NodeTypeSummary:     0.95,
	NodeTypeObservation: 0.8,
}

// ContextNode represents a single node in the context tree.
type ContextNode struct {
	ID       ids.UUID
	ParentID *ids.UUID
	Children []*ContextNode
	Type     NodeType
	Content  string
	// Terms holds pre-computed lexical terms for Jaccard similarity.
	Terms     []string
	Embedding []float32
	CreatedAt time.Time
	// Access tracking for frequency scoring.
	AccessCount  int
	LastAccessed time.Time
	// Scoring weights (populated by ScoreNode).
	SemanticScore  float64
	TemporalScore  float64
	FrequencyScore float64
	TypePrior      float64
	// Final computed score (combined).
	TotalScore float64
}

// IsRoot returns true if this node has no parent.
func (n *ContextNode) IsRoot() bool {
	return n.ParentID == nil || n.ParentID.IsZero()
}

// AddChild adds a child node and sets its parent.
func (n *ContextNode) AddChild(child *ContextNode) {
	child.ParentID = &n.ID
	n.Children = append(n.Children, child)
}

// ContextTree manages a hierarchical context structure with scoring.
type ContextTree struct {
	Root      *ContextNode
	NodeIndex map[ids.UUID]*ContextNode
	Config    ScoringConfig
	mu        sync.RWMutex
}

// ScoringConfig holds parameters for the scoring function S(node, query).
type ScoringConfig struct {
	// Alpha is the semantic weight vs lexical weight (default 0.7).
	// S_semantic = cosine similarity
	// S_lexical = Jaccard similarity
	// Combined: alpha * s_sem + (1-alpha) * s_lex
	Alpha float64

	// Lambda is the temporal decay constant (default ln(2)/6 hours for 6h half-life).
	// w_time = exp(-lambda * delta_t)
	Lambda float64

	// Gamma is the branch inheritance factor (default 0.8).
	// Child scores are boosted by parent score * gamma.
	Gamma float64

	// Tau is the pruning threshold (default 0.3).
	// Nodes below this score are candidates for pruning.
	Tau float64

	// Epsilon is the hysteresis band (default 0.05).
	// Prevents flicker by keeping nodes whose score hasn't changed significantly.
	Epsilon float64

	// BoltzmannTemp is the temperature for sampling (default 0.15-0.3).
	// Higher = more randomness in selection.
	BoltzmannTemp float64
}

// DefaultScoringConfig returns sensible defaults for scoring.
func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{
		Alpha:         0.7,
		Lambda:        math.Ln2 / (6.0 * 3600.0), // 6 hour half-life in seconds
		Gamma:         0.8,
		Tau:           0.3,
		Epsilon:       0.05,
		BoltzmannTemp: 0.2,
	}
}

// NewContextTree creates a new context tree with the given config.
func NewContextTree(config ScoringConfig) *ContextTree {
	rootID := ids.New()
	root := &ContextNode{
		ID:        rootID,
		Type:      NodeTypeRoot,
		Content:   "root",
		CreatedAt: time.Now(),
		TypePrior: TypePriorities[NodeTypeRoot],
	}
	return &ContextTree{
		Root:      root,
		NodeIndex: map[ids.UUID]*ContextNode{rootID: root},
		Config:    config,
	}
}

// AddNode adds a new node to the tree under the specified parent.
func (t *ContextTree) AddNode(parentID ids.UUID, nodeType NodeType, content string, embedding []float32, terms []string) *ContextNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	parent, ok := t.NodeIndex[parentID]
	if !ok {
		parent = t.Root
	}

	node := &ContextNode{
		ID:           ids.New(),
		ParentID:     &parentID,
		Type:         nodeType,
		Content:      content,
		Terms:        terms,
		Embedding:    embedding,
		CreatedAt:    time.Now(),
		LastAccessed: time.Now(),
		AccessCount:  1,
		TypePrior:    TypePriorities[nodeType],
	}

	parent.AddChild(node)
	t.NodeIndex[node.ID] = node
	return node
}

// GetNode retrieves a node by ID.
func (t *ContextTree) GetNode(id ids.UUID) *ContextNode {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.NodeIndex[id]
}

// RecordAccess updates access statistics for a node.
func (t *ContextTree) RecordAccess(id ids.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if node, ok := t.NodeIndex[id]; ok {
		node.AccessCount++
		node.LastAccessed = time.Now()
	}
}

// ScoreNode computes the total score S(node, query) for a node.
//
// Formula: S = (α·s_sem + (1-α)·s_lex) × w_time × w_freq × w_type
//
// Where:
//   - s_sem = cosine similarity of embeddings
//   - s_lex = Jaccard similarity of terms
//   - w_time = exp(-λ·Δt) where Δt is seconds since creation
//   - w_freq = 1 + log(1 + accessCount)
//   - w_type = prior by node type
func (t *ContextTree) ScoreNode(node *ContextNode, queryEmbedding []float32, queryTerms []string) float64 {
	now := time.Now()

	// Semantic score: cosine similarity.
	var sSem float64
	if len(node.Embedding) > 0 && len(queryEmbedding) > 0 {
		sSem = cosineSimilarity(node.Embedding, queryEmbedding)
	}

	// Lexical score: Jaccard similarity of terms.
	var sLex float64
	if len(node.Terms) > 0 && len(queryTerms) > 0 {
		sLex = jaccardSimilarity(node.Terms, queryTerms)
	}

	// Combined semantic + lexical.
	sCombined := t.Config.Alpha*sSem + (1-t.Config.Alpha)*sLex

	// Temporal decay weight.
	deltaT := now.Sub(node.CreatedAt).Seconds()
	wTime := math.Exp(-t.Config.Lambda * deltaT)

	// Frequency weight (log scale to avoid runaway growth).
	wFreq := 1.0 + math.Log1p(float64(node.AccessCount))

	// Type prior.
	wType := node.TypePrior

	// Final combined score.
	total := sCombined * wTime * wFreq * wType

	// Store component scores for debugging/analysis.
	node.SemanticScore = sSem
	node.TemporalScore = wTime
	node.FrequencyScore = wFreq
	node.TotalScore = total

	return total
}

// ScoreAll computes scores for all nodes in the tree.
func (t *ContextTree) ScoreAll(queryEmbedding []float32, queryTerms []string) map[ids.UUID]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	scores := make(map[ids.UUID]float64, len(t.NodeIndex))
	for id, node := range t.NodeIndex {
		scores[id] = t.ScoreNode(node, queryEmbedding, queryTerms)
	}
	return scores
}

// PruneWithTemperature performs Boltzmann sampling to select nodes.
//
// The Boltzmann distribution: P(keep) ∝ exp(S/T)
// Higher temperature = more randomness, lower = more deterministic.
//
// Strategy:
// 1. Always keep nodes above Tau + Epsilon (high confidence)
// 2. Sample from nodes between Tau and Tau + Epsilon using Boltzmann
// 3. Occasionally sample from below Tau to avoid missing rare gems
func (t *ContextTree) PruneWithTemperature(queryEmbedding []float32, queryTerms []string, budget int) []*ContextNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Score all nodes.
	scored := make([]*ContextNode, 0, len(t.NodeIndex))
	for _, node := range t.NodeIndex {
		if node.Type == NodeTypeRoot {
			continue // Never prune root
		}
		t.ScoreNode(node, queryEmbedding, queryTerms)
		scored = append(scored, node)
	}

	// Partition nodes by score relative to threshold.
	var high, mid, low []*ContextNode
	for _, node := range scored {
		if node.TotalScore >= t.Config.Tau+t.Config.Epsilon {
			high = append(high, node)
		} else if node.TotalScore >= t.Config.Tau {
			mid = append(mid, node)
		} else {
			low = append(low, node)
		}
	}

	// Always include high-confidence nodes.
	selected := make([]*ContextNode, len(high))
	copy(selected, high)

	remaining := budget - len(selected)
	if remaining <= 0 {
		return selected[:budget]
	}

	// Use Boltzmann sampling for mid and low pools.
	candidates := append(mid, low...)
	if len(candidates) == 0 {
		return selected
	}

	// Boltzmann probabilities: exp(S/T) / sum(exp(S/T))
	probs := make([]float64, len(candidates))
	var sum float64
	for i, node := range candidates {
		p := math.Exp(node.TotalScore / t.Config.BoltzmannTemp)
		probs[i] = p
		sum += p
	}

	// Normalize and sample without replacement.
	for i := range probs {
		probs[i] /= sum
	}

	// Reservoir sampling based on Boltzmann weights.
	sampled := boltzmannSample(candidates, probs, remaining)
	selected = append(selected, sampled...)

	return selected
}

// SelectNodesWithHysteresis selects nodes while preventing flickering.
//
// Hysteresis rule: If a node was in the previous selection with score S_prev,
// keep it when |S - S_prev| ≤ ε (within the hysteresis band).
//
// This prevents nodes from rapidly entering/leaving the context window
// when their scores fluctuate slightly.
func (t *ContextTree) SelectNodesWithHysteresis(queryEmbedding []float32, queryTerms []string, budget int, prevSelection map[ids.UUID]float64) []*ContextNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Score all nodes.
	scored := make([]*ContextNode, 0, len(t.NodeIndex))
	for _, node := range t.NodeIndex {
		if node.Type == NodeTypeRoot {
			continue
		}
		t.ScoreNode(node, queryEmbedding, queryTerms)
		scored = append(scored, node)
	}

	// Separate into sticky (hysteresis) and regular nodes.
	var sticky, regular []*ContextNode
	for _, node := range scored {
		prevScore, wasSelected := prevSelection[node.ID]
		if wasSelected && math.Abs(node.TotalScore-prevScore) <= t.Config.Epsilon {
			// Node stays selected due to hysteresis.
			sticky = append(sticky, node)
		} else {
			regular = append(regular, node)
		}
	}

	// Sort regular nodes by score descending.
	for i := range regular {
		for j := i + 1; j < len(regular); j++ {
			if regular[j].TotalScore > regular[i].TotalScore {
				regular[i], regular[j] = regular[j], regular[i]
			}
		}
	}

	// Fill budget: first sticky nodes, then top regular nodes.
	selected := make([]*ContextNode, 0, budget)
	selected = append(selected, sticky...)

	remaining := budget - len(selected)
	if remaining > 0 && len(regular) > 0 {
		if remaining > len(regular) {
			remaining = len(regular)
		}
		selected = append(selected, regular[:remaining]...)
	}

	return selected
}

// GetNodesAboveThreshold returns all nodes with score >= threshold.
func (t *ContextTree) GetNodesAboveThreshold(queryEmbedding []float32, queryTerms []string, threshold float64) []*ContextNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []*ContextNode
	for _, node := range t.NodeIndex {
		if node.Type == NodeTypeRoot {
			continue
		}
		score := t.ScoreNode(node, queryEmbedding, queryTerms)
		if score >= threshold {
			result = append(result, node)
		}
	}
	return result
}

// ExportScores returns a snapshot of all node scores for external analysis.
func (t *ContextTree) ExportScores() map[ids.UUID]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	scores := make(map[ids.UUID]float64, len(t.NodeIndex))
	for id, node := range t.NodeIndex {
		scores[id] = node.TotalScore
	}
	return scores
}

// --- Helper functions ---

// cosineSimilarity computes the cosine similarity between two float32 vectors.
// Returns a value in [-1, 1], but typically [0, 1] for normalized embeddings.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		aa := float64(a[i])
		bb := float64(b[i])
		dot += aa * bb
		normA += aa * aa
		normB += bb * bb
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// jaccardSimilarity computes the Jaccard similarity between two string sets.
// J(A, B) = |A ∩ B| / |A ∪ B|
func jaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// Build sets.
	setA := make(map[string]struct{}, len(a))
	for _, s := range a {
		setA[strings.ToLower(s)] = struct{}{}
	}

	setB := make(map[string]struct{}, len(b))
	for _, s := range b {
		setB[strings.ToLower(s)] = struct{}{}
	}

	// Count intersection.
	intersection := 0
	for s := range setA {
		if _, ok := setB[s]; ok {
			intersection++
		}
	}

	// Union size = |A| + |B| - |A ∩ B|
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// boltzmannSample performs weighted random sampling without replacement.
func boltzmannSample(nodes []*ContextNode, probs []float64, n int) []*ContextNode {
	if n >= len(nodes) {
		return nodes
	}
	if n <= 0 {
		return nil
	}

	// Copy for mutation.
	remainingNodes := make([]*ContextNode, len(nodes))
	copy(remainingNodes, nodes)
	remainingProbs := make([]float64, len(probs))
	copy(remainingProbs, probs)

	selected := make([]*ContextNode, 0, n)

	for i := 0; i < n && len(remainingNodes) > 0; i++ {
		// Normalize remaining probabilities.
		var sum float64
		for _, p := range remainingProbs {
			sum += p
		}
		if sum == 0 {
			break
		}

		// Weighted random selection.
		target := rand.Float64() * sum
		var cum float64
		idx := 0
		for j, p := range remainingProbs {
			cum += p
			if cum >= target {
				idx = j
				break
			}
		}

		selected = append(selected, remainingNodes[idx])

		// Remove selected element.
		remainingNodes = append(remainingNodes[:idx], remainingNodes[idx+1:]...)
		remainingProbs = append(remainingProbs[:idx], remainingProbs[idx+1:]...)
	}

	return selected
}

// ExtractTerms extracts lowercase terms from content for lexical matching.
func ExtractTerms(content string) []string {
	words := strings.FieldsFunc(content, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})

	// Deduplicate and lowercase.
	seen := make(map[string]struct{}, len(words))
	terms := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ToLower(w)
		if len(w) > 2 && containsLetter(w) {
			if _, ok := seen[w]; !ok {
				seen[w] = struct{}{}
				terms = append(terms, w)
			}
		}
	}
	return terms
}

func containsLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
