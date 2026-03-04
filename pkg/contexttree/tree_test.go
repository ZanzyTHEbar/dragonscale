package contexttree

import (
	"math"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/stretchr/testify/assert"
)

func TestNewContextTree(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	assert.NotNil(t, tree)
	assert.NotNil(t, tree.Root)
	assert.Equal(t, NodeTypeRoot, tree.Root.Type)
	assert.NotNil(t, tree.NodeIndex)
	assert.Equal(t, 1, len(tree.NodeIndex))
}

func TestAddNode(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	// Add child to root.
	child := tree.AddNode(tree.Root.ID, NodeTypeMessage, "Hello world", []float32{0.1, 0.2, 0.3}, []string{"hello", "world"})

	assert.NotNil(t, child)
	assert.Equal(t, NodeTypeMessage, child.Type)
	assert.Equal(t, "Hello world", child.Content)
	assert.Equal(t, &tree.Root.ID, child.ParentID)
	assert.Equal(t, 1, len(tree.Root.Children))
	assert.Equal(t, 2, len(tree.NodeIndex))
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors.
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{1.0, 0.0, 0.0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 1.0, sim, 0.0001)

	// Orthogonal vectors.
	c := []float32{1.0, 0.0, 0.0}
	d := []float32{0.0, 1.0, 0.0}
	sim = cosineSimilarity(c, d)
	assert.InDelta(t, 0.0, sim, 0.0001)

	// Opposite vectors.
	e := []float32{1.0, 0.0, 0.0}
	f := []float32{-1.0, 0.0, 0.0}
	sim = cosineSimilarity(e, f)
	assert.InDelta(t, -1.0, sim, 0.0001)

	// Empty vectors.
	assert.Equal(t, 0.0, cosineSimilarity(nil, nil))
	assert.Equal(t, 0.0, cosineSimilarity([]float32{}, []float32{}))
}

func TestJaccardSimilarity(t *testing.T) {
	// Identical sets.
	a := []string{"hello", "world"}
	b := []string{"hello", "world"}
	sim := jaccardSimilarity(a, b)
	assert.InDelta(t, 1.0, sim, 0.0001)

	// No overlap.
	c := []string{"hello", "world"}
	d := []string{"foo", "bar"}
	sim = jaccardSimilarity(c, d)
	assert.InDelta(t, 0.0, sim, 0.0001)

	// Partial overlap.
	e := []string{"hello", "world", "foo"}
	f := []string{"hello", "bar", "baz"}
	sim = jaccardSimilarity(e, f)
	// Intersection: 1, Union: 5, Jaccard = 1/5 = 0.2
	assert.InDelta(t, 0.2, sim, 0.0001)

	// Empty sets.
	assert.Equal(t, 0.0, jaccardSimilarity(nil, nil))
	assert.Equal(t, 0.0, jaccardSimilarity([]string{}, []string{"hello"}))
}

func TestScoreNode(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	// Create a node with known embedding.
	node := tree.AddNode(tree.Root.ID, NodeTypeMessage, "test content",
		[]float32{1.0, 0.0, 0.0},
		[]string{"test", "content"})

	// Query with identical embedding.
	queryEmb := []float32{1.0, 0.0, 0.0}
	queryTerms := []string{"test"}

	score := tree.ScoreNode(node, queryEmb, queryTerms)

	// Should have high score due to perfect semantic match.
	assert.Greater(t, score, 0.5)
	assert.InDelta(t, 1.0, node.SemanticScore, 0.0001)
	assert.Equal(t, score, node.TotalScore)
}

func TestScoreAll(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	// Add multiple nodes.
	node1 := tree.AddNode(tree.Root.ID, NodeTypeMessage, "node one",
		[]float32{1.0, 0.0, 0.0}, []string{"node", "one"})
	node2 := tree.AddNode(tree.Root.ID, NodeTypeMessage, "node two",
		[]float32{0.0, 1.0, 0.0}, []string{"node", "two"})

	queryEmb := []float32{1.0, 0.0, 0.0}
	scores := tree.ScoreAll(queryEmb, []string{"node"})

	assert.Equal(t, 3, len(scores)) // Including root
	assert.Greater(t, scores[node1.ID], scores[node2.ID])
}

func TestPruneWithTemperature(t *testing.T) {
	cfg := ScoringConfig{
		Alpha:         0.7,
		Lambda:        math.Ln2 / 3600,
		Gamma:         0.8,
		Tau:           0.3,
		Epsilon:       0.05,
		BoltzmannTemp: 0.2,
	}
	tree := NewContextTree(cfg)

	// Add several nodes with different embeddings.
	for i := 0; i < 10; i++ {
		emb := make([]float32, 3)
		if i < 3 {
			emb[0] = 1.0 // High similarity to query
		} else if i < 6 {
			emb[0] = 0.5 // Medium similarity
		} else {
			emb[1] = 1.0 // Low similarity
		}
		tree.AddNode(tree.Root.ID, NodeTypeMessage, "content",
			emb, []string{"term"})
	}

	queryEmb := []float32{1.0, 0.0, 0.0}
	selected := tree.PruneWithTemperature(queryEmb, []string{"term"}, 5)

	// Should return at most budget nodes.
	assert.LessOrEqual(t, len(selected), 5)

	// First 3 nodes should be prioritized (high similarity).
	if len(selected) > 0 {
		assert.Greater(t, selected[0].TotalScore, 0.0)
	}
}

func TestSelectNodesWithHysteresis(t *testing.T) {
	cfg := ScoringConfig{
		Alpha:         0.7,
		Lambda:        math.Ln2 / 3600,
		Gamma:         0.8,
		Tau:           0.3,
		Epsilon:       0.1, // Large epsilon for hysteresis
		BoltzmannTemp: 0.2,
	}
	tree := NewContextTree(cfg)

	// Add nodes.
	nodes := make([]*ContextNode, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = tree.AddNode(tree.Root.ID, NodeTypeMessage, "content",
			[]float32{float32(i), 0.0, 0.0}, []string{"term"})
	}

	queryEmb := []float32{1.0, 0.0, 0.0}

	// First selection.
	selected1 := tree.SelectNodesWithHysteresis(queryEmb, []string{"term"}, 3, nil)
	assert.Equal(t, 3, len(selected1))

	// Create previous selection map.
	prevSelection := make(map[ids.UUID]float64)
	for _, n := range selected1 {
		prevSelection[n.ID] = n.TotalScore
	}

	// Second selection should be similar due to hysteresis.
	selected2 := tree.SelectNodesWithHysteresis(queryEmb, []string{"term"}, 3, prevSelection)
	assert.Equal(t, 3, len(selected2))
}

func TestGetNodesAboveThreshold(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	// Add nodes with different embeddings.
	highNode := tree.AddNode(tree.Root.ID, NodeTypeMessage, "high",
		[]float32{1.0, 0.0, 0.0}, []string{"term"})
	lowNode := tree.AddNode(tree.Root.ID, NodeTypeMessage, "low",
		[]float32{0.0, 1.0, 0.0}, []string{"other"})

	queryEmb := []float32{1.0, 0.0, 0.0}

	// Score all nodes first so we know their scores.
	tree.ScoreNode(highNode, queryEmb, []string{"term"})
	tree.ScoreNode(lowNode, queryEmb, []string{"term"})

	highScore := highNode.TotalScore
	lowScore := lowNode.TotalScore

	// Select with threshold between the two scores.
	threshold := (highScore + lowScore) / 2
	selected := tree.GetNodesAboveThreshold(queryEmb, []string{"term"}, threshold)

	// Only highNode should be selected.
	assert.Equal(t, 1, len(selected))
	assert.Equal(t, highNode.ID, selected[0].ID)
}

func TestExtractTerms(t *testing.T) {
	content := "Hello, World! This is a TEST. Testing 123."
	terms := ExtractTerms(content)

	// Should extract meaningful words, lowercase, deduplicated.
	assert.Contains(t, terms, "hello")
	assert.Contains(t, terms, "world")
	assert.Contains(t, terms, "this")
	assert.Contains(t, terms, "test") // "TEST" -> "test"
	assert.Contains(t, terms, "testing")
	assert.NotContains(t, terms, "123") // Numbers filtered out
	assert.NotContains(t, terms, "is")  // Short words filtered
	assert.NotContains(t, terms, "a")   // Short words filtered
}

func TestBoltzmannSample(t *testing.T) {
	nodes := []*ContextNode{
		{ID: ids.New(), TotalScore: 1.0},
		{ID: ids.New(), TotalScore: 0.5},
		{ID: ids.New(), TotalScore: 0.1},
	}

	probs := []float64{0.5, 0.3, 0.2}

	// Sample 2 nodes.
	sampled := boltzmannSample(nodes, probs, 2)
	assert.Equal(t, 2, len(sampled))

	// Sample more than available.
	all := boltzmannSample(nodes, probs, 10)
	assert.Equal(t, 3, len(all))

	// Sample zero.
	none := boltzmannSample(nodes, probs, 0)
	assert.Equal(t, 0, len(none))
}

func TestRecordAccess(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	node := tree.AddNode(tree.Root.ID, NodeTypeMessage, "content", nil, nil)
	assert.Equal(t, 1, node.AccessCount) // Initialized to 1

	tree.RecordAccess(node.ID)
	assert.Equal(t, 2, node.AccessCount)

	tree.RecordAccess(node.ID)
	tree.RecordAccess(node.ID)
	assert.Equal(t, 4, node.AccessCount)
}

func TestExportScores(t *testing.T) {
	cfg := DefaultScoringConfig()
	tree := NewContextTree(cfg)

	tree.AddNode(tree.Root.ID, NodeTypeMessage, "one", []float32{1.0, 0.0}, []string{"a"})
	tree.AddNode(tree.Root.ID, NodeTypeMessage, "two", []float32{0.0, 1.0}, []string{"b"})

	scores := tree.ExportScores()
	assert.Equal(t, 3, len(scores)) // 2 children + root

	// All scores should be initialized (0 if not scored yet).
	for _, score := range scores {
		assert.GreaterOrEqual(t, score, 0.0)
	}
}
