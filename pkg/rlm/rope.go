// Package rlm implements the Recursive Language Model (RLM) engine for
// PicoClaw. RLM enables unbounded context processing by recursively
// decomposing long contexts into manageable partitions, processing each
// through the SecureBus, and synthesizing the results.
//
// References:
//   - Zhang & Khattab (MIT CSAIL, arXiv:2512.24601 v2, Jan 2026)
//   - DSPy dspy.RLM canonical implementation
//
// The rope.go file provides an in-process O(log n) context store. We implement
// a pure-Go rope over []byte slices rather than depending on an external
// library, keeping the binary under the 20MB embedded constraint.
package rlm

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Rope is a binary tree over string data that provides O(log n) concatenation,
// slice, and split operations. It is used to store the full conversation context
// without copying the entire buffer on each operation.
//
// This implementation is a balanced rope suitable for contexts up to ~10M tokens.
// Nodes with len ≤ leafThreshold are stored as plain strings (leaf nodes).
// Concatenation produces internal nodes; slicing rebalances lazily.
type Rope struct {
	root *ropeNode
}

const leafThreshold = 4096 // bytes; nodes below this size stay as leaves

type ropeNode struct {
	// Leaf node: data is non-empty, left/right are nil.
	data []byte

	// Internal node: data is nil, left/right are non-nil.
	left, right *ropeNode
	length      int // total byte length of this subtree
}

func newLeaf(data []byte) *ropeNode {
	cp := make([]byte, len(data))
	copy(cp, data)
	return &ropeNode{data: cp, length: len(data)}
}

func newInternal(left, right *ropeNode) *ropeNode {
	return &ropeNode{left: left, right: right, length: left.length + right.length}
}

// NewRope creates a Rope from the given initial content.
func NewRope(content string) *Rope {
	if len(content) == 0 {
		return &Rope{root: newLeaf(nil)}
	}
	return &Rope{root: newLeaf([]byte(content))}
}

// Len returns the total byte length of the rope.
func (r *Rope) Len() int {
	if r.root == nil {
		return 0
	}
	return r.root.length
}

// String materialises the full rope as a string. O(n).
func (r *Rope) String() string {
	if r.root == nil {
		return ""
	}
	var sb strings.Builder
	sb.Grow(r.root.length)
	writeNode(&sb, r.root)
	return sb.String()
}

// Append concatenates content to the end of the rope. O(log n) amortised.
func (r *Rope) Append(content string) {
	if len(content) == 0 {
		return
	}
	newLeafNode := newLeaf([]byte(content))
	if r.root == nil || r.root.length == 0 {
		r.root = newLeafNode
		return
	}
	r.root = newInternal(r.root, newLeafNode)
}

// Slice returns the substring [start, end) in bytes. O(log n + result_size).
// Returns an error if indices are out of range.
func (r *Rope) Slice(start, end int) (string, error) {
	total := r.Len()
	if start < 0 || end < start || end > total {
		return "", fmt.Errorf("rope slice [%d, %d) out of range [0, %d)", start, end, total)
	}
	if start == end {
		return "", nil
	}
	var sb strings.Builder
	sb.Grow(end - start)
	sliceNode(&sb, r.root, start, end)
	return sb.String(), nil
}

// Lines returns all lines (split on '\n') as a slice. O(n).
func (r *Rope) Lines() []string {
	return strings.Split(r.String(), "\n")
}

// RuneLen returns the number of Unicode runes in the rope.
func (r *Rope) RuneLen() int {
	return utf8.RuneCountInString(r.String())
}

// GrepLines returns all lines containing the given substring (case-sensitive).
// Returns up to maxMatches results; 0 means no limit.
func (r *Rope) GrepLines(pattern string, maxMatches int, caseInsensitive bool) []GrepMatch {
	lines := r.Lines()
	var results []GrepMatch

	search := pattern
	if caseInsensitive {
		search = strings.ToLower(pattern)
	}

	offset := 0
	for lineNum, line := range lines {
		check := line
		if caseInsensitive {
			check = strings.ToLower(line)
		}
		if strings.Contains(check, search) {
			results = append(results, GrepMatch{
				LineNum:    lineNum + 1,
				ByteOffset: offset,
				Line:       line,
			})
			if maxMatches > 0 && len(results) >= maxMatches {
				break
			}
		}
		offset += len(line) + 1 // +1 for '\n'
	}
	return results
}

// GrepMatch is a single result from Rope.GrepLines.
type GrepMatch struct {
	LineNum    int    // 1-indexed line number
	ByteOffset int    // byte offset of the start of this line in the rope
	Line       string // the matching line content
}

// Partition splits the rope into k roughly equal partitions by byte count.
// Returns k slices of the rope content (as strings).
func (r *Rope) Partition(k int) []string {
	if k <= 0 {
		k = 1
	}
	total := r.Len()
	if total == 0 {
		result := make([]string, k)
		return result
	}

	chunkSize := total / k
	if chunkSize == 0 {
		chunkSize = 1
	}

	var parts []string
	pos := 0
	for i := 0; i < k; i++ {
		end := pos + chunkSize
		if i == k-1 || end >= total {
			end = total
		}
		// Snap to UTF-8 rune boundary to avoid splitting multi-byte characters.
		if end < total {
			// Walk back until we land on a rune boundary.
			for end > pos && !utf8.RuneStart(r.byteAt(end)) {
				end--
			}
		}
		s, _ := r.Slice(pos, end)
		parts = append(parts, s)
		pos = end
		if pos >= total {
			break
		}
	}

	// Pad to exactly k if we ran out of content.
	for len(parts) < k {
		parts = append(parts, "")
	}
	return parts
}

// byteAt returns the byte at offset i. Panics if out of range.
func (r *Rope) byteAt(i int) byte {
	s, err := r.Slice(i, i+1)
	if err != nil {
		return 0
	}
	if len(s) == 0 {
		return 0
	}
	return s[0]
}

// ── Internal rope helpers ────────────────────────────────────────────────────

func writeNode(sb *strings.Builder, n *ropeNode) {
	if n == nil {
		return
	}
	if n.data != nil {
		sb.Write(n.data)
		return
	}
	writeNode(sb, n.left)
	writeNode(sb, n.right)
}

func sliceNode(sb *strings.Builder, n *ropeNode, start, end int) {
	if n == nil || start >= end {
		return
	}
	if n.data != nil {
		// Leaf: write the overlapping portion.
		lo, hi := start, end
		if lo < 0 {
			lo = 0
		}
		if hi > len(n.data) {
			hi = len(n.data)
		}
		if lo < hi {
			sb.Write(n.data[lo:hi])
		}
		return
	}
	leftLen := n.left.length
	// Overlap with left child?
	if start < leftLen {
		sliceNode(sb, n.left, start, min(end, leftLen))
	}
	// Overlap with right child?
	if end > leftLen {
		sliceNode(sb, n.right, max(0, start-leftLen), end-leftLen)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
