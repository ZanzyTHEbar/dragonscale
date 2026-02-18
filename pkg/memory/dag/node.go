package dag

import (
	"fmt"
	"strings"
)

// Level represents the compression tier of a DAG node.
type Level int

const (
	LevelRaw     Level = 0 // Original messages (not stored as nodes)
	LevelChunk   Level = 1 // Chunk summary (~8-16 messages)
	LevelSection Level = 2 // Section summary (group of chunks)
	LevelSession Level = 3 // Session summary (top-level)
)

func (l Level) String() string {
	switch l {
	case LevelRaw:
		return "raw"
	case LevelChunk:
		return "chunk"
	case LevelSection:
		return "section"
	case LevelSession:
		return "session"
	default:
		return fmt.Sprintf("level-%d", l)
	}
}

// Node is a single node in the compression DAG. Each node stores an
// extractive summary and retains lossless pointers back to the
// original message range it covers.
type Node struct {
	ID       string `json:"id"`
	Level    Level  `json:"level"`
	Summary  string `json:"summary"`
	Tokens   int    `json:"tokens"`
	StartIdx int    `json:"start_idx"` // Inclusive index into original message slice
	EndIdx   int    `json:"end_idx"`   // Exclusive index into original message slice
	Children []string `json:"children,omitempty"` // Child node IDs (lower level)
}

// MessageRange returns the [start, end) range of original messages this node covers.
func (n *Node) MessageRange() (int, int) {
	return n.StartIdx, n.EndIdx
}

// Span returns how many original messages this node covers.
func (n *Node) Span() int {
	return n.EndIdx - n.StartIdx
}

// FormatForPrompt renders the node as a compact block for context injection.
func (n *Node) FormatForPrompt() string {
	return fmt.Sprintf("[%s msgs %d-%d] %s", n.Level, n.StartIdx, n.EndIdx-1, n.Summary)
}

// DAG is the hierarchical compression tree. Nodes at higher levels
// summarize groups of lower-level nodes. The root level covers the
// entire session.
type DAG struct {
	Nodes map[string]*Node `json:"nodes"`
	Roots []string         `json:"roots"` // Top-level node IDs (highest compression)
}

// NewDAG creates an empty DAG.
func NewDAG() *DAG {
	return &DAG{
		Nodes: make(map[string]*Node),
	}
}

// Add inserts a node into the DAG.
func (d *DAG) Add(node *Node) {
	d.Nodes[node.ID] = node
}

// SetRoots sets the top-level node IDs.
func (d *DAG) SetRoots(ids []string) {
	d.Roots = ids
}

// Get returns a node by ID, or nil if not found.
func (d *DAG) Get(id string) *Node {
	return d.Nodes[id]
}

// NodesAtLevel returns all nodes at the given compression level, ordered by StartIdx.
func (d *DAG) NodesAtLevel(level Level) []*Node {
	var result []*Node
	for _, n := range d.Nodes {
		if n.Level == level {
			result = append(result, n)
		}
	}
	sortByStart(result)
	return result
}

// TotalTokens returns the sum of tokens across all nodes at the given level.
func (d *DAG) TotalTokens(level Level) int {
	total := 0
	for _, n := range d.Nodes {
		if n.Level == level {
			total += n.Tokens
		}
	}
	return total
}

// FormatLevel renders all nodes at a given level as a prompt-ready string.
func (d *DAG) FormatLevel(level Level) string {
	nodes := d.NodesAtLevel(level)
	if len(nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(n.FormatForPrompt())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func sortByStart(nodes []*Node) {
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j].StartIdx < nodes[j-1].StartIdx; j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
}
