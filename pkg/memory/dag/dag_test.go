package dag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeMessages(n int) []Message {
	msgs := make([]Message, n)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = Message{
			Role:    role,
			Content: strings.Repeat("word ", 20) + ".",
		}
	}
	return msgs
}

func TestCompressor_EmptyInput(t *testing.T) {
	c := NewCompressor(DefaultCompressorConfig())
	d := c.Compress(nil)
	assert.Empty(t, d.Nodes)
	assert.Empty(t, d.Roots)
}

func TestCompressor_SmallInput(t *testing.T) {
	c := NewCompressor(DefaultCompressorConfig())
	msgs := []Message{
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing well. Thanks for asking!"},
	}
	d := c.Compress(msgs)

	require.Len(t, d.Nodes, 1)
	chunk := d.NodesAtLevel(LevelChunk)
	require.Len(t, chunk, 1)
	assert.Equal(t, 0, chunk[0].StartIdx)
	assert.Equal(t, 2, chunk[0].EndIdx)
	assert.Contains(t, chunk[0].Summary, "user:")
	assert.Contains(t, chunk[0].Summary, "assistant:")
}

func TestCompressor_ChunkSplitting(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	c := NewCompressor(cfg)

	msgs := makeMessages(12)
	d := c.Compress(msgs)

	chunks := d.NodesAtLevel(LevelChunk)
	require.Len(t, chunks, 3)

	assert.Equal(t, 0, chunks[0].StartIdx)
	assert.Equal(t, 4, chunks[0].EndIdx)
	assert.Equal(t, 4, chunks[1].StartIdx)
	assert.Equal(t, 8, chunks[1].EndIdx)
	assert.Equal(t, 8, chunks[2].StartIdx)
	assert.Equal(t, 12, chunks[2].EndIdx)
}

func TestCompressor_SectionBuilding(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	cfg.SectionSize = 2
	c := NewCompressor(cfg)

	// 20 messages = 5 chunks, section_size=2 => 3 sections
	msgs := makeMessages(20)
	d := c.Compress(msgs)

	chunks := d.NodesAtLevel(LevelChunk)
	assert.Len(t, chunks, 5)

	sections := d.NodesAtLevel(LevelSection)
	assert.Len(t, sections, 3)

	// First section covers chunks 0-1 (msgs 0-7)
	assert.Equal(t, 0, sections[0].StartIdx)
	assert.Equal(t, 8, sections[0].EndIdx)
	assert.Len(t, sections[0].Children, 2)
}

func TestCompressor_SessionSummary(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	cfg.SectionSize = 2
	c := NewCompressor(cfg)

	// 24 messages => 6 chunks => 3 sections => 1 session
	msgs := makeMessages(24)
	d := c.Compress(msgs)

	sessions := d.NodesAtLevel(LevelSession)
	require.Len(t, sessions, 1)
	assert.Equal(t, 0, sessions[0].StartIdx)
	assert.Equal(t, 24, sessions[0].EndIdx)
	assert.Len(t, sessions[0].Children, 3)

	require.Len(t, d.Roots, 1)
	assert.Equal(t, sessions[0].ID, d.Roots[0])
}

func TestExtractSentences(t *testing.T) {
	tests := []struct {
		name string
		text string
		n    int
		want string
	}{
		{"single sentence", "Hello world.", 1, "Hello world."},
		{"two sentences", "First sentence. Second sentence.", 2, "First sentence. Second sentence."},
		{"extract one from many", "A. B. C. D.", 1, "A."},
		{"empty", "", 1, ""},
		{"no period", "Hello world", 1, "Hello world"},
		{"question mark", "What? Who knows.", 1, "What?"},
		{"exclamation", "Wow! Amazing.", 2, "Wow! Amazing."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSentences(tt.text, tt.n)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractSentences_Truncation(t *testing.T) {
	long := strings.Repeat("This is a very long sentence with many words. ", 20)
	result := extractSentences(long, 5)
	assert.LessOrEqual(t, len([]rune(result)), 210)
	assert.True(t, strings.HasSuffix(result, "..."))
}

func TestNode_FormatForPrompt(t *testing.T) {
	n := &Node{
		ID:       "chunk-1",
		Level:    LevelChunk,
		Summary:  "User asked about auth. Assistant explained JWT flow.",
		StartIdx: 0,
		EndIdx:   8,
	}
	formatted := n.FormatForPrompt()
	assert.Contains(t, formatted, "[chunk msgs 0-7]")
	assert.Contains(t, formatted, "User asked about auth")
}

func TestDAG_FormatLevel(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	c := NewCompressor(cfg)

	msgs := makeMessages(8)
	d := c.Compress(msgs)

	output := d.FormatLevel(LevelChunk)
	assert.NotEmpty(t, output)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 2)
}

func TestDAG_TotalTokens(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	c := NewCompressor(cfg)

	msgs := makeMessages(12)
	d := c.Compress(msgs)

	chunkTokens := d.TotalTokens(LevelChunk)
	assert.Greater(t, chunkTokens, 0)
}

func TestComputeBudget(t *testing.T) {
	cfg := DefaultBudgetConfig()
	b := ComputeBudget(100000, cfg)

	assert.Equal(t, 100000, b.Total)
	assert.Equal(t, 20000, b.SystemPrompt)
	assert.Equal(t, 10000, b.Observations)
	assert.Equal(t, 5000, b.Knowledge)
	assert.Equal(t, 25000, b.DAGSummaries)
	assert.Equal(t, 30000, b.RawTail)
	assert.Equal(t, 10000, b.ToolResults)
}

func TestBudget_Remaining(t *testing.T) {
	b := Budget{Total: 10000}
	assert.Equal(t, 7000, b.Remaining(1000, 500, 500, 500, 500, 0))
	assert.Equal(t, 0, b.Remaining(5000, 3000, 1000, 1000, 1000, 0))
}

func TestSelectDAGLevel(t *testing.T) {
	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	cfg.SectionSize = 2
	c := NewCompressor(cfg)

	msgs := makeMessages(24)
	d := c.Compress(msgs)

	chunkTokens := d.TotalTokens(LevelChunk)

	// Large budget -> most detailed (chunk)
	assert.Equal(t, LevelChunk, SelectDAGLevel(d, chunkTokens+1000))

	// Very small budget -> session level
	assert.Equal(t, LevelSession, SelectDAGLevel(d, 10))

	// Nil DAG
	assert.Equal(t, LevelRaw, SelectDAGLevel(nil, 1000))
}

func TestTailMessageCount(t *testing.T) {
	assert.Equal(t, 4, TailMessageCount(100))   // Minimum
	assert.Equal(t, 20, TailMessageCount(1000))  // 1000/50
	assert.Equal(t, 4, TailMessageCount(0))      // Zero budget
	assert.Equal(t, 4, TailMessageCount(-1))     // Negative
}

func TestRenderDAGForBudget(t *testing.T) {
	assert.Empty(t, RenderDAGForBudget(nil, 1000))

	cfg := DefaultCompressorConfig()
	cfg.ChunkSize = 4
	cfg.SectionSize = 2
	c := NewCompressor(cfg)

	// 16 messages => 4 chunks => 2 sections => 1 session
	msgs := makeMessages(16)
	d := c.Compress(msgs)

	// Large budget should get chunk-level detail
	chunkTokens := d.TotalTokens(LevelChunk)
	output := RenderDAGForBudget(d, chunkTokens+1000)
	assert.NotEmpty(t, output)
	assert.Contains(t, output, "[chunk")
}
