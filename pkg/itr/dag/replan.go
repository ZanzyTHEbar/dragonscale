package dag

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/itr"
)

// ReplanConfig configures the iterative replanning loop.
type ReplanConfig struct {
	MaxReplans int // maximum replanning iterations; 0 → 3
}

// DefaultReplanConfig returns sensible defaults.
func DefaultReplanConfig() ReplanConfig {
	return ReplanConfig{MaxReplans: 3}
}

// ReplanResult holds the combined output of an iterative DAG execution with
// optional replanning passes.
type ReplanResult struct {
	FinalAnswer string
	TotalTokens uint32
	Iterations  int
}

// ReplanLoop runs the DAG executor with iterative replanning. After each
// execution, the Joiner evaluates whether the answer is complete. If not,
// it triggers another planning pass with the previous results as context.
//
// The loop terminates when:
//   - The Joiner's answer does not contain the replan sentinel, or
//   - MaxReplans iterations are exhausted, or
//   - An error occurs.
func ReplanLoop(
	ctx context.Context,
	executor *Executor,
	planner *Planner,
	sessionKey string,
	query string,
	availableTools []string,
	cfg ReplanConfig,
) (*ReplanResult, error) {
	maxReplans := cfg.MaxReplans
	if maxReplans <= 0 {
		maxReplans = 3
	}

	var totalTokens uint32
	previousResults := ""

	for i := 0; i <= maxReplans; i++ {
		planQuery := query
		if previousResults != "" {
			planQuery = fmt.Sprintf(
				"Previous execution produced partial results:\n%s\n\nOriginal task: %s\n\nPlan additional steps to complete the task.",
				truncate(previousResults, 4000), query)
		}

		plan, planTokens, err := planner.Plan(ctx, planQuery, availableTools)
		if err != nil {
			return nil, fmt.Errorf("replan iteration %d: planning failed: %w", i, err)
		}
		totalTokens += planTokens

		result, err := executor.Execute(ctx, sessionKey, plan)
		if err != nil {
			return nil, fmt.Errorf("replan iteration %d: execution failed: %w", i, err)
		}
		totalTokens += result.TotalTokens

		if !needsReplan(result.FinalAnswer) {
			return &ReplanResult{
				FinalAnswer: result.FinalAnswer,
				TotalTokens: totalTokens,
				Iterations:  i + 1,
			}, nil
		}

		previousResults = result.FinalAnswer
	}

	return &ReplanResult{
		FinalAnswer: previousResults,
		TotalTokens: totalTokens,
		Iterations:  maxReplans + 1,
	}, nil
}

const replanSentinel = "[NEEDS_MORE_STEPS]"

// needsReplan checks whether the Joiner's output indicates the task is
// incomplete and requires another planning pass.
func needsReplan(answer string) bool {
	return findIndex(answer, replanSentinel) >= 0
}

// ExecuteWithReplan is a convenience wrapper that runs a pre-built DAGPlan
// through the executor, then optionally replans if the Joiner signals
// incompleteness.
func ExecuteWithReplan(
	ctx context.Context,
	executor *Executor,
	planner *Planner,
	sessionKey string,
	initialPlan *itr.DAGPlan,
	query string,
	availableTools []string,
	cfg ReplanConfig,
) (*ReplanResult, error) {
	maxReplans := cfg.MaxReplans
	if maxReplans <= 0 {
		maxReplans = 3
	}

	var totalTokens uint32

	result, err := executor.Execute(ctx, sessionKey, initialPlan)
	if err != nil {
		return nil, err
	}
	totalTokens += result.TotalTokens

	if !needsReplan(result.FinalAnswer) {
		return &ReplanResult{
			FinalAnswer: result.FinalAnswer,
			TotalTokens: totalTokens,
			Iterations:  1,
		}, nil
	}

	replanResult, err := ReplanLoop(ctx, executor, planner, sessionKey, query, availableTools, cfg)
	if err != nil {
		return nil, err
	}
	replanResult.TotalTokens += totalTokens
	return replanResult, nil
}
