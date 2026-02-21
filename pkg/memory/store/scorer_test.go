package store

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeuristicScorer_BasicScoring(t *testing.T) {
	scorer := NewHeuristicScorer()
	ctx := context.Background()

	tests := []struct {
		name      string
		content   string
		role      string
		minImport float64
		maxImport float64
	}{
		{"greeting", "hello", "user", 0.0, 0.3},
		{"important fact", "Remember: the API key must always be rotated every 90 days. This is a critical security requirement.", "system", 0.5, 1.0},
		{"code content", "```go\nfunc main() { fmt.Println(\"hello\") }\n```", "assistant", 0.3, 1.0},
		{"tool result", "Found 15 matching files in the src/ directory", "tool", 0.3, 0.8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scorer.Score(ctx, tt.content, tt.role, "")
			require.NoError(t, err)
			assert.GreaterOrEqual(t, result.Importance, tt.minImport, "importance too low")
			assert.LessOrEqual(t, result.Importance, tt.maxImport, "importance too high")
			assert.InDelta(t, 0.5, result.Salience, 0.001, "heuristic salience should be 0.5")
		})
	}
}

func TestHeuristicScorer_SectorClassification(t *testing.T) {
	scorer := NewHeuristicScorer()
	ctx := context.Background()

	tests := []struct {
		name    string
		content string
		sector  memory.Sector
	}{
		{
			"procedural",
			"Step 1: Install Go. Step 2: Run go mod init. Then execute the command to build.",
			memory.SectorProcedural,
		},
		{
			"semantic",
			"The interface defines a struct type with a function method. The API module provides class definitions.",
			memory.SectorSemantic,
		},
		{
			"reflective",
			"I think this is a lesson learned from our observation. In my opinion this insight and realization changes our approach. This reflection reveals a pattern.",
			memory.SectorReflective,
		},
		{
			"episodic default",
			"We had a chat about random things yesterday.",
			memory.SectorEpisodic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scorer.Score(ctx, tt.content, "user", "")
			require.NoError(t, err)
			assert.Equal(t, tt.sector, result.Sector)
		})
	}
}

func TestParseScoringResponse_ValidJSON(t *testing.T) {
	input := `{"importance": 0.85, "salience": 0.6, "sector": "semantic"}`
	result, err := parseScoringResponse(input)
	require.NoError(t, err)
	assert.InDelta(t, 0.85, result.Importance, 0.001)
	assert.InDelta(t, 0.6, result.Salience, 0.001)
	assert.Equal(t, memory.SectorSemantic, result.Sector)
}

func TestParseScoringResponse_WithCodeFences(t *testing.T) {
	input := "```json\n{\"importance\": 0.9, \"salience\": 0.3, \"sector\": \"procedural\"}\n```"
	result, err := parseScoringResponse(input)
	require.NoError(t, err)
	assert.InDelta(t, 0.9, result.Importance, 0.001)
	assert.Equal(t, memory.SectorProcedural, result.Sector)
}

func TestParseScoringResponse_ClampsValues(t *testing.T) {
	input := `{"importance": 1.5, "salience": -0.3, "sector": "episodic"}`
	result, err := parseScoringResponse(input)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, result.Importance, 0.001, "should clamp to 1.0")
	assert.InDelta(t, 0.0, result.Salience, 0.001, "should clamp to 0.0")
}

func TestParseScoringResponse_UnknownSector(t *testing.T) {
	input := `{"importance": 0.5, "salience": 0.5, "sector": "unknown_sector"}`
	result, err := parseScoringResponse(input)
	require.NoError(t, err)
	assert.Equal(t, memory.SectorEpisodic, result.Sector, "unknown sector should default to episodic")
}

func TestNormalizeSector(t *testing.T) {
	tests := []struct {
		input    string
		expected memory.Sector
	}{
		{"episodic", memory.SectorEpisodic},
		{"SEMANTIC", memory.SectorSemantic},
		{" procedural ", memory.SectorProcedural},
		{"Reflective", memory.SectorReflective},
		{"garbage", memory.SectorEpisodic},
		{"", memory.SectorEpisodic},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeSector(tt.input))
		})
	}
}
