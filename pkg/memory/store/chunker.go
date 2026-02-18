// Package store provides the Memory logic layer implementations.
package store

import (
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// MarkdownChunker splits markdown text at structural boundaries (headings,
// code fences, paragraphs) while respecting a target chunk size. It tracks
// heading hierarchy so each chunk carries its section context.
type MarkdownChunker struct {
	chunkSize    int
	chunkOverlap int
	codeBlocks   bool
	headings     bool
}

// MarkdownChunkerConfig controls chunking behavior.
type MarkdownChunkerConfig struct {
	ChunkSize    int  // Target chunk size in characters. Default: 1600 (~400 tokens)
	ChunkOverlap int  // Overlap between chunks in characters. Default: 320 (~80 tokens)
	CodeBlocks   bool // Preserve code block boundaries. Default: true
	Headings     bool // Track heading hierarchy. Default: true
}

// DefaultMarkdownChunkerConfig returns sensible defaults for RAG chunking.
func DefaultMarkdownChunkerConfig() MarkdownChunkerConfig {
	return MarkdownChunkerConfig{
		ChunkSize:    1600,
		ChunkOverlap: 320,
		CodeBlocks:   true,
		Headings:     true,
	}
}

// NewMarkdownChunker creates a Chunker backed by a lightweight markdown splitter.
func NewMarkdownChunker(cfg MarkdownChunkerConfig) *MarkdownChunker {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1600
	}
	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = 0
	}
	return &MarkdownChunker{
		chunkSize:    cfg.ChunkSize,
		chunkOverlap: cfg.ChunkOverlap,
		codeBlocks:   cfg.CodeBlocks,
		headings:     cfg.Headings,
	}
}

// Chunk splits content into chunks using the markdown-aware splitter.
func (c *MarkdownChunker) Chunk(content string) ([]memory.ChunkResult, error) {
	if content == "" {
		return nil, nil
	}

	sections := c.splitIntoSections(content)
	merged := c.mergeSections(sections)

	results := make([]memory.ChunkResult, len(merged))
	for i, text := range merged {
		results[i] = memory.ChunkResult{Text: text, Index: i}
	}
	return results, nil
}

// section represents a contiguous block of markdown text with optional heading context.
type section struct {
	heading string // accumulated heading hierarchy (e.g. "# Foo\n## Bar")
	body    string
}

// splitIntoSections breaks markdown into structural sections delineated by
// headings and fenced code blocks.
func (c *MarkdownChunker) splitIntoSections(content string) []section {
	lines := strings.Split(content, "\n")
	var sections []section

	var headingStack [6]string // h1..h6
	var curBody strings.Builder
	inFence := false

	flush := func() {
		body := strings.TrimSpace(curBody.String())
		if body == "" {
			return
		}
		var heading string
		if c.headings {
			heading = buildHeadingContext(headingStack[:])
		}
		sections = append(sections, section{heading: heading, body: body})
		curBody.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				if c.codeBlocks {
					curBody.WriteString(line)
					curBody.WriteByte('\n')
				}
				inFence = false
				continue
			}
			inFence = true
			if c.codeBlocks {
				curBody.WriteString(line)
				curBody.WriteByte('\n')
			}
			continue
		}

		if inFence {
			if c.codeBlocks {
				curBody.WriteString(line)
				curBody.WriteByte('\n')
			}
			continue
		}

		if level := headingLevel(trimmed); level > 0 {
			flush()
			headingStack[level-1] = trimmed
			for i := level; i < 6; i++ {
				headingStack[i] = ""
			}
			continue
		}

		curBody.WriteString(line)
		curBody.WriteByte('\n')
	}

	if inFence && c.codeBlocks {
		// unclosed fence — still flush what we have
	}
	flush()
	return sections
}

// mergeSections combines small sections up to chunkSize, then splits any
// oversized sections with recursive character splitting.
func (c *MarkdownChunker) mergeSections(sections []section) []string {
	var chunks []string
	var curChunk strings.Builder
	var lastOverlap string

	flushChunk := func() {
		text := strings.TrimSpace(curChunk.String())
		if text == "" {
			return
		}
		chunks = append(chunks, text)
		if c.chunkOverlap > 0 {
			lastOverlap = overlapTail(text, c.chunkOverlap)
		}
		curChunk.Reset()
	}

	for _, sec := range sections {
		full := sec.body
		if sec.heading != "" {
			full = sec.heading + "\n" + sec.body
		}

		if runeLen(full) > c.chunkSize {
			flushChunk()
			subChunks := recursiveSplit(full, c.chunkSize, c.chunkOverlap)
			chunks = append(chunks, subChunks...)
			if c.chunkOverlap > 0 && len(subChunks) > 0 {
				lastOverlap = overlapTail(subChunks[len(subChunks)-1], c.chunkOverlap)
			}
			continue
		}

		cur := curChunk.String()
		combined := cur + "\n" + full
		if cur != "" && runeLen(combined) > c.chunkSize {
			flushChunk()
			if lastOverlap != "" {
				curChunk.WriteString(lastOverlap)
				curChunk.WriteByte('\n')
			}
		}
		if curChunk.Len() > 0 {
			curChunk.WriteByte('\n')
		}
		curChunk.WriteString(full)
	}
	flushChunk()
	return chunks
}

// headingLevel returns 1-6 for markdown ATX headings, 0 otherwise.
func headingLevel(line string) int {
	if !strings.HasPrefix(line, "#") {
		return 0
	}
	level := 0
	for _, ch := range line {
		if ch == '#' {
			level++
		} else {
			break
		}
	}
	if level > 6 {
		return 0
	}
	if len(line) > level && line[level] != ' ' {
		return 0
	}
	return level
}

func buildHeadingContext(stack []string) string {
	var parts []string
	for _, h := range stack {
		if h != "" {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, "\n")
}

// recursiveSplit splits text using progressively finer separators.
func recursiveSplit(text string, chunkSize, overlap int) []string {
	separators := []string{"\n\n", "\n", " "}
	return doRecursiveSplit(text, separators, chunkSize, overlap)
}

func doRecursiveSplit(text string, separators []string, chunkSize, overlap int) []string {
	if runeLen(text) <= chunkSize {
		t := strings.TrimSpace(text)
		if t == "" {
			return nil
		}
		return []string{t}
	}

	if len(separators) == 0 {
		t := strings.TrimSpace(text)
		if t == "" {
			return nil
		}
		return []string{t}
	}

	sep := separators[0]
	remaining := separators[1:]
	parts := strings.Split(text, sep)

	var chunks []string
	var current strings.Builder

	for _, part := range parts {
		candidate := current.String()
		if candidate != "" {
			candidate += sep
		}
		candidate += part

		if runeLen(candidate) > chunkSize && current.Len() > 0 {
			cur := strings.TrimSpace(current.String())
			if cur != "" {
				if runeLen(cur) > chunkSize {
					chunks = append(chunks, doRecursiveSplit(cur, remaining, chunkSize, overlap)...)
				} else {
					chunks = append(chunks, cur)
				}
			}
			current.Reset()
			if overlap > 0 {
				tail := overlapTail(cur, overlap)
				if tail != "" {
					current.WriteString(tail)
					current.WriteString(sep)
				}
			}
		}
		if current.Len() > 0 {
			current.WriteString(sep)
		}
		current.WriteString(part)
	}

	if rest := strings.TrimSpace(current.String()); rest != "" {
		if runeLen(rest) > chunkSize {
			chunks = append(chunks, doRecursiveSplit(rest, remaining, chunkSize, overlap)...)
		} else {
			chunks = append(chunks, rest)
		}
	}

	return chunks
}

func overlapTail(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[len(runes)-n:])
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
