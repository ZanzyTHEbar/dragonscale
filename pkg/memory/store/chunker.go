// Package store provides the Memory logic layer implementations.
package store

import (
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/tmc/langchaingo/textsplitter"
)

// MarkdownChunker wraps langchaingo's MarkdownTextSplitter to implement memory.Chunker.
type MarkdownChunker struct {
	splitter *textsplitter.MarkdownTextSplitter
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

// NewMarkdownChunker creates a Chunker backed by langchaingo's MarkdownTextSplitter.
func NewMarkdownChunker(cfg MarkdownChunkerConfig) *MarkdownChunker {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1600
	}
	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = 0
	}

	return &MarkdownChunker{
		splitter: textsplitter.NewMarkdownTextSplitter(
			textsplitter.WithChunkSize(cfg.ChunkSize),
			textsplitter.WithChunkOverlap(cfg.ChunkOverlap),
			textsplitter.WithCodeBlocks(cfg.CodeBlocks),
			textsplitter.WithHeadingHierarchy(cfg.Headings),
		),
	}
}

// Chunk splits content into chunks using the markdown-aware splitter.
func (c *MarkdownChunker) Chunk(content string) ([]memory.ChunkResult, error) {
	parts, err := c.splitter.SplitText(content)
	if err != nil {
		return nil, err
	}

	results := make([]memory.ChunkResult, len(parts))
	for i, part := range parts {
		results[i] = memory.ChunkResult{
			Text:  part,
			Index: i,
		}
	}
	return results, nil
}
