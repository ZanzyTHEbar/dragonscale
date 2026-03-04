package cortex

import (
	"math"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// TaskBaseline tracks running statistics for task performance using Welford's online algorithm.
// This enables incremental calculation of mean and variance without storing all historical data.
type TaskBaseline struct {
	Count               int
	MeanTokens          float64
	MeanErrors          float64
	MeanUserCorrections float64
	M2Tokens            float64 // sum of squares of differences from mean (for variance)
	M2Errors            float64
	M2UserCorrections   float64
}

// RetrievedMemory represents a memory retrieved from the memory system with its similarity score.
type RetrievedMemory struct {
	MemoryID        ids.UUID
	Similarity      float64
	SelfReportScore *int // nullable 0-3 scale
}

// UpdateWeight applies exponential moving average (EMA) with clamping.
// Formula: weight_new = (1 - α) × weight_old + α × credit
// Result is clamped to [0.1, 5.0].
// Default learningRate (α) is 0.1.
func UpdateWeight(oldWeight, credit, learningRate float64) float64 {
	// Use default learning rate if not provided (0 or negative)
	alpha := learningRate
	if alpha <= 0 {
		alpha = 0.1
	}

	// EMA formula: new = (1 - α) * old + α * credit
	newWeight := (1-alpha)*oldWeight + alpha*credit

	// Clamp to [0.1, 5.0]
	const minWeight = 0.1
	const maxWeight = 5.0

	if newWeight < minWeight {
		return minWeight
	}
	if newWeight > maxWeight {
		return maxWeight
	}

	return newWeight
}

// ComputeCredit calculates per-memory credit from task outcome.
// Formula: credit = task_score × (self_report / 3.0) × (1.0 / max(num_memories, 1))
// selfReportScore is on a 0-3 scale.
func ComputeCredit(taskScore float64, selfReportScore, numMemories int) float64 {
	// Normalize self-report to [0, 1]
	selfReportNorm := float64(selfReportScore) / 3.0

	// Avoid division by zero for numMemories
	n := numMemories
	if n < 1 {
		n = 1
	}
	inverseMemories := 1.0 / float64(n)

	// Credit formula
	credit := taskScore * selfReportNorm * inverseMemories

	return credit
}

// ComputeTaskScore calculates a z-score based task performance score.
// For cold start (baseline count < 10): uses simple deltas from mean.
// For normal operation: uses z-score calculation.
// Returns positive values for good performance, negative for poor performance.
func ComputeTaskScore(baseline *TaskBaseline, tokens, errors, userCorrections int, completed bool) float64 {
	if baseline == nil {
		// No baseline: neutral score if completed, penalize if not
		if completed {
			return 0.0
		}
		return -1.0
	}

	// Cold start: simple delta-based scoring
	if baseline.Count < 10 {
		return computeColdStartScore(baseline, tokens, errors, userCorrections, completed)
	}

	// Normal operation: z-score based scoring
	return computeZScore(baseline, tokens, errors, userCorrections, completed)
}

// computeColdStartScore handles the cold start scenario with simple deltas.
// Lower tokens = good, lower errors = good, lower corrections = good.
func computeColdStartScore(baseline *TaskBaseline, tokens, errors, userCorrections int, completed bool) float64 {
	score := 0.0

	// Completion is the primary signal - heavy weight on completion
	if completed {
		score += 1.0
	} else {
		score -= 1.0
	}

	// Fresh baseline (no established means): use absolute thresholds
	if baseline.Count == 0 || (baseline.MeanTokens == 0 && baseline.MeanErrors == 0 && baseline.MeanUserCorrections == 0) {
		// Absolute scoring for fresh baselines
		// Fewer tokens is better (normalized by assuming reasonable range)
		score -= float64(tokens) / 1000.0 * 0.1

		// Error penalty
		score -= float64(errors) * 0.2

		// Correction penalty
		score -= float64(userCorrections) * 0.2

		return score
	}

	// With established baseline: use deltas
	// Token efficiency: fewer tokens than mean is better
	if baseline.MeanTokens > 0 {
		tokenDelta := float64(tokens) - baseline.MeanTokens
		score -= tokenDelta / baseline.MeanTokens * 0.3
	}

	// Error penalty: compare to mean
	if baseline.MeanErrors > 0 || errors > 0 {
		errorDelta := float64(errors) - baseline.MeanErrors
		score -= errorDelta * 0.2
	}

	// Correction penalty: compare to mean
	if baseline.MeanUserCorrections > 0 || userCorrections > 0 {
		correctionDelta := float64(userCorrections) - baseline.MeanUserCorrections
		score -= correctionDelta * 0.2
	}

	return score
}

// computeZScore calculates standardized z-scores for task performance.
// Good performance = fewer tokens, fewer errors, fewer corrections.
func computeZScore(baseline *TaskBaseline, tokens, errors, userCorrections int, completed bool) float64 {
	score := 0.0

	// Completion is primary signal - heavy penalty ensures incomplete tasks score negative
	if completed {
		score += 1.0
	} else {
		score -= 10.0 // Very heavy penalty for incomplete tasks - can't be overcome by good metrics
	}

	// Calculate z-scores (negative z-score is better for tokens/errors/corrections)
	tokenStdDev := StdDev(baseline.M2Tokens, baseline.Count)
	errorStdDev := StdDev(baseline.M2Errors, baseline.Count)
	correctionStdDev := StdDev(baseline.M2UserCorrections, baseline.Count)

	// Token efficiency z-score (fewer tokens = better, so negative z-score is positive contribution)
	if tokenStdDev > 0 {
		tokenZ := (float64(tokens) - baseline.MeanTokens) / tokenStdDev
		score -= tokenZ * 0.5 // Subtract because lower tokens is better
	}

	// Error z-score (fewer errors = better)
	if errorStdDev > 0 {
		errorZ := (float64(errors) - baseline.MeanErrors) / errorStdDev
		score -= errorZ * 0.8 // Errors are more heavily weighted
	}

	// Correction z-score (fewer corrections = better)
	if correctionStdDev > 0 {
		correctionZ := (float64(userCorrections) - baseline.MeanUserCorrections) / correctionStdDev
		score -= correctionZ * 0.7
	}

	return score
}

// UpdateBaseline updates running statistics using Welford's online algorithm.
// This allows incremental calculation of mean and variance without storing all data points.
// Formula (Welford's):
//
//	n = count + 1
//	δ = x - mean
//	mean = mean + δ/n
//	M2 = M2 + δ × (x - new_mean)
func UpdateBaseline(baseline *TaskBaseline, tokens, errors, userCorrections int) *TaskBaseline {
	if baseline == nil {
		// Initialize new baseline with first observation
		return &TaskBaseline{
			Count:               1,
			MeanTokens:          float64(tokens),
			MeanErrors:          float64(errors),
			MeanUserCorrections: float64(userCorrections),
			M2Tokens:            0,
			M2Errors:            0,
			M2UserCorrections:   0,
		}
	}

	// Create a copy to avoid mutating the original
	b := &TaskBaseline{
		Count:               baseline.Count,
		MeanTokens:          baseline.MeanTokens,
		MeanErrors:          baseline.MeanErrors,
		MeanUserCorrections: baseline.MeanUserCorrections,
		M2Tokens:            baseline.M2Tokens,
		M2Errors:            baseline.M2Errors,
		M2UserCorrections:   baseline.M2UserCorrections,
	}

	// Increment count
	n := b.Count + 1
	b.Count = n

	// Update tokens statistics
	b.MeanTokens, b.M2Tokens = welfordUpdate(b.MeanTokens, b.M2Tokens, float64(tokens), n)

	// Update errors statistics
	b.MeanErrors, b.M2Errors = welfordUpdate(b.MeanErrors, b.M2Errors, float64(errors), n)

	// Update corrections statistics
	b.MeanUserCorrections, b.M2UserCorrections = welfordUpdate(b.MeanUserCorrections, b.M2UserCorrections, float64(userCorrections), n)

	return b
}

// welfordUpdate performs a single step of Welford's online algorithm.
// Returns updated mean and M2.
func welfordUpdate(mean, m2, x float64, n int) (float64, float64) {
	// δ = x - mean
	delta := x - mean

	// mean = mean + δ/n
	newMean := mean + delta/float64(n)

	// M2 = M2 + δ × (x - new_mean)
	newM2 := m2 + delta*(x-newMean)

	return newMean, newM2
}

// StdDev calculates standard deviation from M2 (sum of squares of differences).
// Formula: σ = sqrt(M2 / (n - 1))
// Returns 1.0 for cold start (n < 2) to avoid division by zero.
func StdDev(m2 float64, count int) float64 {
	// Cold start: avoid division by zero
	if count < 2 {
		return 1.0
	}

	// Population variance for stability with small samples
	// Using n-1 for sample standard deviation
	variance := m2 / float64(count-1)

	// Ensure non-negative variance (numerical precision issues)
	if variance < 0 {
		variance = 0
	}

	return math.Sqrt(variance)
}

// max returns the larger of a and b.
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
