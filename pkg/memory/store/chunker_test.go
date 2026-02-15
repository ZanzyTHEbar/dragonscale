package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownChunker_BasicSplit(t *testing.T) {
	chunker := NewMarkdownChunker(MarkdownChunkerConfig{
		ChunkSize:    100,
		ChunkOverlap: 20,
	})

	content := strings.Repeat("This is a test sentence. ", 20) // ~500 chars
	chunks, err := chunker.Chunk(content)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1, "long content should produce multiple chunks")

	for i, c := range chunks {
		assert.Equal(t, i, c.Index)
		assert.NotEmpty(t, c.Text)
	}
}

func TestMarkdownChunker_SmallContent(t *testing.T) {
	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())

	chunks, err := chunker.Chunk("Short text.")
	require.NoError(t, err)
	assert.Len(t, chunks, 1)
	assert.Equal(t, "Short text.", chunks[0].Text)
}

func TestMarkdownChunker_PreservesMarkdownStructure(t *testing.T) {
	chunker := NewMarkdownChunker(MarkdownChunkerConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
		CodeBlocks:   true,
		Headings:     true,
	})

	content := `# Section 1

This is the first section with some content that explains things.

## Subsection 1.1

More detailed content goes here with code examples.

` + "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\n" + `
# Section 2

Another section with completely different content about a different topic.

## Subsection 2.1

Even more content follows here with additional details and explanations that make the text longer.
`

	chunks, err := chunker.Chunk(content)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1)

	// All chunks should have content
	for _, c := range chunks {
		assert.NotEmpty(t, c.Text, "chunk %d is empty", c.Index)
	}

	// Reassemble should cover all content
	var allText strings.Builder
	for _, c := range chunks {
		allText.WriteString(c.Text)
	}
	reassembled := allText.String()
	assert.Contains(t, reassembled, "Section 1")
	assert.Contains(t, reassembled, "Section 2")
	assert.Contains(t, reassembled, "fmt.Println")
}

func TestMarkdownChunker_EmptyContent(t *testing.T) {
	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())
	chunks, err := chunker.Chunk("")
	require.NoError(t, err)
	assert.Empty(t, chunks)
}

func TestMarkdownChunker_DefaultConfig(t *testing.T) {
	cfg := DefaultMarkdownChunkerConfig()
	assert.Equal(t, 1600, cfg.ChunkSize)
	assert.Equal(t, 320, cfg.ChunkOverlap)
	assert.True(t, cfg.CodeBlocks)
	assert.True(t, cfg.Headings)
}
