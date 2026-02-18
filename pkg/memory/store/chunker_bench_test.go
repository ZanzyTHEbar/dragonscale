package store

import (
	"strings"
	"testing"
)

func BenchmarkMarkdownChunker_SmallDoc(b *testing.B) {
	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())
	content := "# Title\n\nA short paragraph."

	b.ReportAllocs()
	for b.Loop() {
		_, _ = chunker.Chunk(content)
	}
}

func BenchmarkMarkdownChunker_MediumDoc(b *testing.B) {
	chunker := NewMarkdownChunker(MarkdownChunkerConfig{
		ChunkSize:    400,
		ChunkOverlap: 80,
		CodeBlocks:   true,
		Headings:     true,
	})

	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("## Section\n\n")
		sb.WriteString(strings.Repeat("Lorem ipsum dolor sit amet. ", 20))
		sb.WriteString("\n\n```go\nfunc foo() {}\n```\n\n")
	}
	content := sb.String()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = chunker.Chunk(content)
	}
}

func BenchmarkMarkdownChunker_LargeDoc(b *testing.B) {
	chunker := NewMarkdownChunker(DefaultMarkdownChunkerConfig())

	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("# Major Section\n\n")
		for j := 0; j < 5; j++ {
			sb.WriteString("## Subsection\n\n")
			sb.WriteString(strings.Repeat("Content paragraph with meaningful text for testing purposes. ", 30))
			sb.WriteString("\n\n")
		}
	}
	content := sb.String()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = chunker.Chunk(content)
	}
}
