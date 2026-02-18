package dag

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Message is a minimal message representation for DAG compression.
type Message struct {
	Role    string
	Content string
}

// CompressorConfig controls the deterministic compression behavior.
type CompressorConfig struct {
	ChunkSize       int // Messages per chunk node (default 8)
	SectionSize     int // Chunks per section node (default 4)
	MaxSentences    int // Max sentences to extract per message (default 2)
	TargetRatio     float64 // Target compression ratio (default 0.25 = 4:1)
}

// DefaultCompressorConfig returns sensible defaults.
func DefaultCompressorConfig() CompressorConfig {
	return CompressorConfig{
		ChunkSize:    8,
		SectionSize:  4,
		MaxSentences: 2,
		TargetRatio:  0.25,
	}
}

// Compressor builds a DAG from raw messages using deterministic
// extractive summarization. No LLM calls — fully reproducible.
type Compressor struct {
	cfg     CompressorConfig
	counter int
}

// NewCompressor creates a new deterministic compressor.
func NewCompressor(cfg CompressorConfig) *Compressor {
	return &Compressor{cfg: cfg}
}

// Compress builds a hierarchical DAG from the given messages.
// Messages are grouped into chunks, chunks into sections, and
// sections into a session summary.
func (c *Compressor) Compress(msgs []Message) *DAG {
	d := NewDAG()
	if len(msgs) == 0 {
		return d
	}

	// Level 1: Chunk summaries
	chunkNodes := c.buildChunks(msgs, d)
	if len(chunkNodes) == 0 {
		return d
	}

	// Level 2: Section summaries (groups of chunks)
	sectionNodes := c.buildSections(chunkNodes, d)

	// Level 3: Session summary (if multiple sections)
	if len(sectionNodes) > 1 {
		sessionNode := c.buildSessionSummary(sectionNodes, d)
		d.SetRoots([]string{sessionNode.ID})
	} else if len(sectionNodes) == 1 {
		d.SetRoots([]string{sectionNodes[0].ID})
	} else {
		ids := make([]string, len(chunkNodes))
		for i, n := range chunkNodes {
			ids[i] = n.ID
		}
		d.SetRoots(ids)
	}

	return d
}

func (c *Compressor) nextID(prefix string) string {
	c.counter++
	return fmt.Sprintf("%s-%d", prefix, c.counter)
}

func (c *Compressor) buildChunks(msgs []Message, d *DAG) []*Node {
	var chunks []*Node
	for i := 0; i < len(msgs); i += c.cfg.ChunkSize {
		end := i + c.cfg.ChunkSize
		if end > len(msgs) {
			end = len(msgs)
		}

		chunk := msgs[i:end]
		summary := c.extractChunkSummary(chunk)
		node := &Node{
			ID:       c.nextID("chunk"),
			Level:    LevelChunk,
			Summary:  summary,
			Tokens:   estimateTokens(summary),
			StartIdx: i,
			EndIdx:   end,
		}
		d.Add(node)
		chunks = append(chunks, node)
	}
	return chunks
}

func (c *Compressor) buildSections(chunks []*Node, d *DAG) []*Node {
	if len(chunks) <= c.cfg.SectionSize {
		return chunks
	}

	var sections []*Node
	for i := 0; i < len(chunks); i += c.cfg.SectionSize {
		end := i + c.cfg.SectionSize
		if end > len(chunks) {
			end = len(chunks)
		}

		group := chunks[i:end]
		childIDs := make([]string, len(group))
		var summaryParts []string
		for j, ch := range group {
			childIDs[j] = ch.ID
			summaryParts = append(summaryParts, ch.Summary)
		}

		combined := strings.Join(summaryParts, " ")
		summary := extractSentences(combined, c.cfg.MaxSentences)
		node := &Node{
			ID:       c.nextID("section"),
			Level:    LevelSection,
			Summary:  summary,
			Tokens:   estimateTokens(summary),
			StartIdx: group[0].StartIdx,
			EndIdx:   group[len(group)-1].EndIdx,
			Children: childIDs,
		}
		d.Add(node)
		sections = append(sections, node)
	}
	return sections
}

func (c *Compressor) buildSessionSummary(sections []*Node, d *DAG) *Node {
	childIDs := make([]string, len(sections))
	var summaryParts []string
	for i, s := range sections {
		childIDs[i] = s.ID
		summaryParts = append(summaryParts, s.Summary)
	}

	combined := strings.Join(summaryParts, " ")
	summary := extractSentences(combined, c.cfg.MaxSentences)
	node := &Node{
		ID:       c.nextID("session"),
		Level:    LevelSession,
		Summary:  summary,
		Tokens:   estimateTokens(summary),
		StartIdx: sections[0].StartIdx,
		EndIdx:   sections[len(sections)-1].EndIdx,
		Children: childIDs,
	}
	d.Add(node)
	return node
}

// extractChunkSummary produces a deterministic summary of a message chunk.
// Strategy: for each message, take the first sentence (or first N chars if short).
// Prefix with role to preserve conversational structure.
func (c *Compressor) extractChunkSummary(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		sentence := extractSentences(m.Content, 1)
		if sentence == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", m.Role, sentence))
	}
	return strings.Join(parts, " | ")
}

// extractSentences pulls the first N sentences from text.
// A sentence ends at '.', '!', '?', or '\n\n'.
func extractSentences(text string, n int) string {
	if n <= 0 || text == "" {
		return ""
	}

	text = strings.TrimSpace(text)
	var result []string
	remaining := text

	for i := 0; i < n && remaining != ""; i++ {
		idx := findSentenceEnd(remaining)
		if idx < 0 {
			result = append(result, strings.TrimSpace(remaining))
			break
		}
		sentence := strings.TrimSpace(remaining[:idx+1])
		if sentence != "" {
			result = append(result, sentence)
		}
		remaining = strings.TrimSpace(remaining[idx+1:])
	}

	joined := strings.Join(result, " ")
	const maxLen = 200
	if utf8.RuneCountInString(joined) > maxLen {
		runes := []rune(joined)
		return string(runes[:maxLen]) + "..."
	}
	return joined
}

func findSentenceEnd(s string) int {
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			return i
		}
		if r == '\n' && i+1 < len(s) && s[i+1] == '\n' {
			return i
		}
	}
	return -1
}

// estimateTokens provides a rough token count (chars * 2/5 heuristic).
func estimateTokens(s string) int {
	return utf8.RuneCountInString(s) * 2 / 5
}
