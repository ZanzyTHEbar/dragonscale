package rlm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// PartitionResult holds the answer produced for one context partition.
type PartitionResult struct {
	PartitionIdx int
	ContextKey   string
	Answer       string
	Err          error
	Tokens       uint32
}

// FanOut executes fn over each partition concurrently and collects results.
// maxConcurrency limits how many goroutines run simultaneously.
// 0 means unbounded (one goroutine per partition).
func FanOut(
	ctx context.Context,
	partitions []string,
	maxConcurrency int,
	fn func(ctx context.Context, idx int, contextKey, partition string) PartitionResult,
) []PartitionResult {
	n := len(partitions)
	if n == 0 {
		return nil
	}

	results := make([]PartitionResult, n)

	if maxConcurrency <= 0 || maxConcurrency >= n {
		// Run all goroutines concurrently.
		var wg sync.WaitGroup
		wg.Add(n)
		for i, part := range partitions {
			i, part := i, part
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("partition-%d", i)
				results[i] = fn(ctx, i, key, part)
			}()
		}
		wg.Wait()
		return results
	}

	// Bounded concurrency via semaphore channel.
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, part := range partitions {
		i, part := i, part
		sem <- struct{}{}
		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()
			key := fmt.Sprintf("partition-%d", i)
			results[i] = fn(ctx, i, key, part)
		}()
	}

	wg.Wait()
	return results
}

// MergeResults synthesises the answers from all partitions into a single string.
// This is the "merge" step of the RLM recursion. A production implementation
// would use another LLM call; this version concatenates non-empty answers with
// newlines and deduplicates.
func MergeResults(results []PartitionResult) string {
	seen := make(map[string]bool)
	var parts []string

	for _, r := range results {
		if r.Err != nil || r.Answer == "" {
			continue
		}
		key := strings.TrimSpace(r.Answer)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, key)
	}

	return strings.Join(parts, "\n")
}

// TotalTokens sums the cost_tokens across all PartitionResults.
func TotalTokens(results []PartitionResult) uint32 {
	var total uint32
	for _, r := range results {
		total += r.Tokens
	}
	return total
}
