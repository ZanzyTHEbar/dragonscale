package rlm

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFanOutEmpty(t *testing.T) {
	results := FanOut(context.Background(), nil, 0, nil)
	assert.Nil(t, results)
}

func TestFanOutUnbounded(t *testing.T) {
	partitions := []string{"part-0", "part-1", "part-2"}

	results := FanOut(context.Background(), partitions, 0, func(ctx context.Context, idx int, key, partition string) PartitionResult {
		return PartitionResult{
			PartitionIdx: idx,
			ContextKey:   key,
			Answer:       fmt.Sprintf("answer for %s", partition),
			Tokens:       10,
		}
	})

	require.Len(t, results, 3)
	for i, r := range results {
		assert.Equal(t, i, r.PartitionIdx)
		assert.Contains(t, r.Answer, fmt.Sprintf("part-%d", i))
		assert.Equal(t, uint32(10), r.Tokens)
	}
}

func TestFanOutBounded(t *testing.T) {
	partitions := make([]string, 10)
	for i := range partitions {
		partitions[i] = fmt.Sprintf("chunk-%d", i)
	}

	var maxConcurrent int64
	var current int64

	results := FanOut(context.Background(), partitions, 3, func(ctx context.Context, idx int, key, partition string) PartitionResult {
		c := atomic.AddInt64(&current, 1)
		for {
			old := atomic.LoadInt64(&maxConcurrent)
			if c <= old {
				break
			}
			if atomic.CompareAndSwapInt64(&maxConcurrent, old, c) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&current, -1)
		return PartitionResult{
			PartitionIdx: idx,
			Answer:       partition,
			Tokens:       1,
		}
	})

	require.Len(t, results, 10)
	assert.LessOrEqual(t, atomic.LoadInt64(&maxConcurrent), int64(3),
		"max concurrent goroutines should respect the limit")
}

func TestFanOutPreservesOrder(t *testing.T) {
	partitions := []string{"A", "B", "C", "D"}

	results := FanOut(context.Background(), partitions, 2, func(ctx context.Context, idx int, key, partition string) PartitionResult {
		return PartitionResult{
			PartitionIdx: idx,
			Answer:       partition,
		}
	})

	require.Len(t, results, 4)
	for i, r := range results {
		assert.Equal(t, i, r.PartitionIdx)
		assert.Equal(t, partitions[i], r.Answer)
	}
}

func TestMergeResultsDeduplication(t *testing.T) {
	results := []PartitionResult{
		{Answer: "  answer one  "},
		{Answer: "answer one"},
		{Answer: "answer two"},
		{Answer: "", Err: fmt.Errorf("failed")},
		{Answer: "answer two"},
	}

	merged := MergeResults(results)
	assert.Equal(t, "answer one\nanswer two", merged)
}

func TestMergeResultsAllErrors(t *testing.T) {
	results := []PartitionResult{
		{Err: fmt.Errorf("e1")},
		{Err: fmt.Errorf("e2")},
	}
	assert.Empty(t, MergeResults(results))
}

func TestMergeResultsAllEmpty(t *testing.T) {
	results := []PartitionResult{
		{Answer: ""},
		{Answer: "  "},
	}
	assert.Empty(t, MergeResults(results))
}

func TestTotalTokens(t *testing.T) {
	results := []PartitionResult{
		{Tokens: 100},
		{Tokens: 250},
		{Tokens: 50},
	}
	assert.Equal(t, uint32(400), TotalTokens(results))
}

func TestTotalTokensEmpty(t *testing.T) {
	assert.Equal(t, uint32(0), TotalTokens(nil))
}
