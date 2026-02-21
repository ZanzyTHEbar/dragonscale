package rlm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStrategyPlanNextFinalAtMaxDepth(t *testing.T) {
	t.Parallel()
	sp := NewStrategyPlanner(StrategyConfig{
		DirectThreshold:   8192,
		DefaultPartitionK: 4,
		MaxDepth:          3,
	})

	op := sp.PlanNext(100000, "any query", 3)
	assert.Equal(t, OpFinal, op.Type)
}

func TestStrategyPlanNextFinalSmallContext(t *testing.T) {
	t.Parallel()
	sp := NewStrategyPlanner(DefaultStrategyConfig())

	op := sp.PlanNext(1000, "any query", 0)
	assert.Equal(t, OpFinal, op.Type)
}

func TestStrategyPlanNextGrepForKeywordQuery(t *testing.T) {
	t.Parallel()
	sp := NewStrategyPlanner(DefaultStrategyConfig())

	tests := []struct {
		query string
	}{
		{`find "handleRequest"`},
		{"search for main function"},
		{"where is the config loaded"},
		{"error: connection refused trace"},
		{"func processData handler"},
		{"def calculate_total in utils"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			op := sp.PlanNext(1_000_000, tt.query, 0)
			assert.Equal(t, OpGrep, op.Type)
			assert.NotEmpty(t, op.GrepQuery)
		})
	}
}

func TestStrategyPlanNextPartitionDefault(t *testing.T) {
	t.Parallel()
	sp := NewStrategyPlanner(DefaultStrategyConfig())
	op := sp.PlanNext(100_000, "summarize this document", 0)
	assert.Equal(t, OpPartition, op.Type)
	assert.Equal(t, 4, op.PartitionK)
}

func TestStrategyPlanNextPartitionLargeContext(t *testing.T) {
	t.Parallel()
	sp := NewStrategyPlanner(DefaultStrategyConfig())
	op := sp.PlanNext(5_000_000, "summarize this corpus", 0)
	assert.Equal(t, OpPartition, op.Type)
	assert.Equal(t, 8, op.PartitionK, "large contexts should use more partitions")
}

func TestExtractKeywordQuoted(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "handleRequest", extractKeyword(`find "handleRequest" in the codebase`))
}

func TestExtractKeywordNoQuotes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "find", extractKeyword("find the main function"))
}

func TestExtractKeywordEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", extractKeyword(""))
}

func TestLooksLikeKeywordQuery(t *testing.T) {
	t.Parallel()
	assert.True(t, looksLikeKeywordQuery(`find "something"`))
	assert.True(t, looksLikeKeywordQuery("error: something broke"))
	assert.True(t, looksLikeKeywordQuery("func processData"))
	assert.True(t, looksLikeKeywordQuery("def main"))
	assert.True(t, looksLikeKeywordQuery("find the bug"))
	assert.True(t, looksLikeKeywordQuery("search for pattern"))
	assert.True(t, looksLikeKeywordQuery("where is the config"))

	assert.False(t, looksLikeKeywordQuery("summarize this"))
	assert.False(t, looksLikeKeywordQuery("how does X work"))
}
