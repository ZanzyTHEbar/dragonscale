package rlm_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/rlm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoModel is a deterministic callModel that returns the first 50 chars
// of the context plus the query, for tracing purposes.
func echoModel(_ context.Context, ctxContent, query string) (string, uint32, error) {
	preview := ctxContent
	if len(preview) > 50 {
		preview = preview[:50]
	}
	return fmt.Sprintf("Q=%s CTX=%s", query, preview), 10, nil
}

// errorModel always returns an error.
func errorModel(_ context.Context, _, _ string) (string, uint32, error) {
	return "", 0, fmt.Errorf("model error")
}

func TestEngine_SmallContext_AnswerDirectly(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultEngineConfig()
	cfg.Strategy.DirectThreshold = 10000 // larger than our test context
	engine := rlm.NewEngine(cfg, nil, echoModel)

	answer, tokens, err := engine.Answer(t.Context(), "sess", "what?", "short context")
	require.NoError(t, err)
	assert.NotEmpty(t, answer)
	assert.Greater(t, tokens, uint32(0))
}

func TestEngine_LargeContext_Partitions(t *testing.T) {
	t.Parallel(
	// Force partitioning by setting DirectThreshold very low.
	)

	cfg := rlm.DefaultEngineConfig()
	cfg.Strategy.DirectThreshold = 10
	cfg.Strategy.DefaultPartitionK = 2
	cfg.Strategy.MaxDepth = 2
	cfg.MaxConcurrency = 2

	largeCtx := strings.Repeat("hello world ", 100) // ~1200 bytes
	engine := rlm.NewEngine(cfg, nil, echoModel)

	answer, tokens, err := engine.Answer(t.Context(), "sess", "summarise", largeCtx)
	require.NoError(t, err)
	assert.NotEmpty(t, answer)
	assert.Greater(t, tokens, uint32(0))
}

func TestEngine_MaxDepthTerminates(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultEngineConfig()
	cfg.Strategy.DirectThreshold = 0 // always partition
	cfg.Strategy.MaxDepth = 3
	cfg.MaxConcurrency = 1

	content := strings.Repeat("x", 500)
	engine := rlm.NewEngine(cfg, nil, echoModel)

	// Should terminate without stack overflow.
	_, _, err := engine.Answer(t.Context(), "sess", "any", content)
	assert.NoError(t, err)
}

func TestEngine_ModelError_Propagates(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultEngineConfig()
	cfg.Strategy.DirectThreshold = 10000
	engine := rlm.NewEngine(cfg, nil, errorModel)

	_, _, err := engine.Answer(t.Context(), "sess", "q", "ctx")
	assert.Error(t, err)
}

func TestEngine_GrepQuery_NarrowsContext(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultEngineConfig()
	cfg.Strategy.DirectThreshold = 10
	cfg.Strategy.DefaultPartitionK = 2
	cfg.Strategy.MaxDepth = 3
	cfg.MaxConcurrency = 1

	// Content has one line matching a grep pattern.
	content := "foo\nfunc myFunction() {}\nbar\nbaz"
	engine := rlm.NewEngine(cfg, nil, echoModel)

	answer, _, err := engine.Answer(t.Context(), "sess", "find func definition", content)
	require.NoError(t, err)
	assert.NotEmpty(t, answer)
}

func TestFanOut_AllPartitions_Processed(t *testing.T) {
	t.Parallel()
	partitions := []string{"A", "B", "C", "D"}
	results := rlm.FanOut(t.Context(), partitions, 2,
		func(_ context.Context, idx int, key, part string) rlm.PartitionResult {
			return rlm.PartitionResult{PartitionIdx: idx, ContextKey: key, Answer: "ans-" + part, Tokens: 1}
		})

	assert.Len(t, results, 4)
	for i, r := range results {
		assert.Equal(t, i, r.PartitionIdx)
		assert.Equal(t, "ans-"+partitions[i], r.Answer)
	}
}

func TestFanOut_UnboundedConcurrency(t *testing.T) {
	t.Parallel()
	parts := make([]string, 20)
	for i := range parts {
		parts[i] = fmt.Sprintf("part-%d", i)
	}
	results := rlm.FanOut(t.Context(), parts, 0,
		func(_ context.Context, idx int, key, part string) rlm.PartitionResult {
			return rlm.PartitionResult{PartitionIdx: idx, Answer: part, Tokens: 2}
		})
	assert.Len(t, results, 20)
	assert.Equal(t, uint32(40), rlm.TotalTokens(results))
}

func TestFanOut_Empty(t *testing.T) {
	t.Parallel()
	results := rlm.FanOut(t.Context(), nil, 4,
		func(_ context.Context, _ int, _, _ string) rlm.PartitionResult {
			return rlm.PartitionResult{}
		})
	assert.Nil(t, results)
}

func TestMergeResults_Deduplication(t *testing.T) {
	t.Parallel()
	results := []rlm.PartitionResult{
		{Answer: "alpha"},
		{Answer: "beta"},
		{Answer: "alpha"}, // duplicate
		{Answer: ""},      // empty — should be skipped
	}
	merged := rlm.MergeResults(results)
	assert.Equal(t, "alpha\nbeta", merged)
}

func TestMergeResults_WithErrors(t *testing.T) {
	t.Parallel()
	results := []rlm.PartitionResult{
		{Answer: "good"},
		{Err: fmt.Errorf("failed"), Answer: "should be skipped"},
	}
	merged := rlm.MergeResults(results)
	assert.Equal(t, "good", merged)
}

func TestStrategyPlanner_SmallContext_OpFinal(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultStrategyConfig()
	cfg.DirectThreshold = 1000
	planner := rlm.NewStrategyPlanner(cfg)

	op := planner.PlanNext(500, "any query", 0)
	assert.Equal(t, rlm.OpFinal, op.Type)
}

func TestStrategyPlanner_MaxDepth_OpFinal(t *testing.T) {
	t.Parallel()
	cfg := rlm.DefaultStrategyConfig()
	cfg.MaxDepth = 3
	planner := rlm.NewStrategyPlanner(cfg)

	op := planner.PlanNext(100000, "any query", 3) // depth == MaxDepth
	assert.Equal(t, rlm.OpFinal, op.Type)
}

func TestStrategyPlanner_KeywordQuery_OpGrep(t *testing.T) {
	t.Parallel()
	planner := rlm.NewStrategyPlanner(rlm.DefaultStrategyConfig())

	// "find" prefix should trigger grep.
	op := planner.PlanNext(100000, "find myFunction in code", 0)
	assert.Equal(t, rlm.OpGrep, op.Type)
	assert.NotEmpty(t, op.GrepQuery)
}

func TestStrategyPlanner_LargeContext_OpPartition(t *testing.T) {
	t.Parallel()
	planner := rlm.NewStrategyPlanner(rlm.DefaultStrategyConfig())

	op := planner.PlanNext(100000, "summarise everything", 0)
	assert.Equal(t, rlm.OpPartition, op.Type)
	assert.Greater(t, op.PartitionK, 0)
}
