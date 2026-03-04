package cortex

import (
	"math"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// TestUpdateWeight verifies EMA calculation and clamping bounds.
func TestUpdateWeight(t *testing.T) {
	tests := []struct {
		name         string
		oldWeight    float64
		credit       float64
		learningRate float64
		want         float64
		description  string
	}{
		{
			name:         "basic EMA calculation",
			oldWeight:    1.0,
			credit:       2.0,
			learningRate: 0.1,
			want:         0.9*1.0 + 0.1*2.0, // 1.1
			description:  "Standard EMA: 0.9*1.0 + 0.1*2.0 = 1.1",
		},
		{
			name:         "default learning rate when zero",
			oldWeight:    1.0,
			credit:       2.0,
			learningRate: 0,
			want:         0.9*1.0 + 0.1*2.0, // 1.1 (uses default 0.1)
			description:  "Should use default α=0.1 when learningRate is 0",
		},
		{
			name:         "default learning rate when negative",
			oldWeight:    1.0,
			credit:       2.0,
			learningRate: -0.5,
			want:         0.9*1.0 + 0.1*2.0, // 1.1 (uses default 0.1)
			description:  "Should use default α=0.1 when learningRate is negative",
		},
		{
			name:         "clamping at lower bound",
			oldWeight:    0.1,
			credit:       -10.0,
			learningRate: 0.5,
			want:         0.1, // clamped to min
			description:  "Should clamp to minimum 0.1",
		},
		{
			name:         "clamping at upper bound",
			oldWeight:    4.0,
			credit:       10.0,
			learningRate: 0.5,
			want:         5.0, // clamped to max
			description:  "Should clamp to maximum 5.0",
		},
		{
			name:         "high learning rate converges fast",
			oldWeight:    1.0,
			credit:       3.0,
			learningRate: 0.9,
			want:         0.1*1.0 + 0.9*3.0, // 2.8
			description:  "High α converges faster toward credit",
		},
		{
			name:         "zero credit reduces weight",
			oldWeight:    2.0,
			credit:       0.0,
			learningRate: 0.1,
			want:         0.9*2.0 + 0.1*0.0, // 1.8
			description:  "Zero credit should reduce weight",
		},
		{
			name:         "weight stays within bounds at boundary",
			oldWeight:    0.1,
			credit:       0.1,
			learningRate: 0.5,
			want:         0.1, // stays at min
			description:  "Should not change when already at minimum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpdateWeight(tt.oldWeight, tt.credit, tt.learningRate)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("UpdateWeight(%v, %v, %v) = %v, want %v (%s)",
					tt.oldWeight, tt.credit, tt.learningRate, got, tt.want, tt.description)
			}
		})
	}
}

// TestComputeCredit verifies the credit formula with various self-report scores.
func TestComputeCredit(t *testing.T) {
	tests := []struct {
		name            string
		taskScore       float64
		selfReportScore int
		numMemories     int
		want            float64
		description     string
	}{
		{
			name:            "basic formula with self-report 3",
			taskScore:       2.0,
			selfReportScore: 3,
			numMemories:     2,
			want:            1.0, // 2.0 * 1.0 * 0.5
			description:     "2.0 * 1.0 * 0.5 = 1.0",
		},
		{
			name:            "self-report 2 scales down credit",
			taskScore:       2.0,
			selfReportScore: 2,
			numMemories:     2,
			want:            0.6666666666666666, // 2.0 * (2/3) * 0.5
			description:     "Self-report 2 should give 2/3 of max credit",
		},
		{
			name:            "self-report 0 gives zero credit",
			taskScore:       2.0,
			selfReportScore: 0,
			numMemories:     2,
			want:            0.0, // 2.0 * 0 * 0.5 = 0
			description:     "Self-report 0 should give zero credit",
		},
		{
			name:            "single memory gets full distribution",
			taskScore:       3.0,
			selfReportScore: 3,
			numMemories:     1,
			want:            3.0 * 1.0 * 1.0, // 3.0
			description:     "Single memory gets full credit",
		},
		{
			name:            "zero numMemories defaults to 1",
			taskScore:       2.0,
			selfReportScore: 3,
			numMemories:     0,
			want:            2.0 * 1.0 * 1.0, // 2.0 (uses max(0,1)=1)
			description:     "Zero memories should default to 1",
		},
		{
			name:            "negative numMemories defaults to 1",
			taskScore:       2.0,
			selfReportScore: 3,
			numMemories:     -5,
			want:            2.0 * 1.0 * 1.0, // 2.0
			description:     "Negative memories should default to 1",
		},
		{
			name:            "many memories distribute credit",
			taskScore:       3.0,
			selfReportScore: 3,
			numMemories:     10,
			want:            3.0 * 1.0 * 0.1, // 0.3
			description:     "Credit distributed across 10 memories",
		},
		{
			name:            "negative task score",
			taskScore:       -1.0,
			selfReportScore: 3,
			numMemories:     1,
			want:            -1.0 * 1.0 * 1.0, // -1.0
			description:     "Negative task score propagates to credit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCredit(tt.taskScore, tt.selfReportScore, tt.numMemories)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("ComputeCredit(%v, %v, %v) = %v, want %v (%s)",
					tt.taskScore, tt.selfReportScore, tt.numMemories, got, tt.want, tt.description)
			}
		})
	}
}

// TestComputeTaskScore verifies both cold start and normal z-score paths.
func TestComputeTaskScore(t *testing.T) {
	tests := []struct {
		name            string
		baseline        *TaskBaseline
		tokens          int
		errors          int
		userCorrections int
		completed       bool
		wantPositive    bool // true = expect positive, false = expect negative
		exactValue      *float64
		description     string
	}{
		{
			name:            "cold start completed task",
			baseline:        &TaskBaseline{Count: 5},
			tokens:          100,
			errors:          0,
			userCorrections: 0,
			completed:       true,
			wantPositive:    true,
			description:     "Cold start with good performance should be positive",
		},
		{
			name:            "cold start incomplete task",
			baseline:        &TaskBaseline{Count: 5},
			tokens:          100,
			errors:          0,
			userCorrections: 0,
			completed:       false,
			wantPositive:    false,
			description:     "Cold start incomplete should be negative",
		},
		{
			name:            "cold start with errors",
			baseline:        &TaskBaseline{Count: 5},
			tokens:          100,
			errors:          5,
			userCorrections: 0,
			completed:       true,
			wantPositive:    false, // errors should make it negative
			description:     "Cold start with many errors should be negative",
		},
		{
			name: "normal z-score good performance",
			baseline: &TaskBaseline{
				Count:               20,
				MeanTokens:          1000,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            10000, // std dev ~22.9
				M2Errors:            100,   // std dev ~2.29
				M2UserCorrections:   40,    // std dev ~1.45
			},
			tokens:          800, // 200 less than mean = good
			errors:          1,   // fewer errors = good
			userCorrections: 0,   // fewer corrections = good
			completed:       true,
			wantPositive:    true,
			description:     "Better than baseline should give positive score",
		},
		{
			name: "normal z-score poor performance",
			baseline: &TaskBaseline{
				Count:               20,
				MeanTokens:          1000,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            10000,
				M2Errors:            100,
				M2UserCorrections:   40,
			},
			tokens:          1500, // more tokens = worse
			errors:          15,   // more errors = worse
			userCorrections: 8,    // more corrections = worse
			completed:       true,
			wantPositive:    false,
			description:     "Worse than baseline should give negative score",
		},
		{
			name: "incomplete task heavy penalty",
			baseline: &TaskBaseline{
				Count:               20,
				MeanTokens:          1000,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            10000,
				M2Errors:            100,
				M2UserCorrections:   40,
			},
			tokens:          800,
			errors:          1,
			userCorrections: 0,
			completed:       false,
			wantPositive:    false,
			description:     "Incomplete task gets heavy penalty even with good metrics",
		},
		{
			name:            "nil baseline completed",
			baseline:        nil,
			tokens:          100,
			errors:          0,
			userCorrections: 0,
			completed:       true,
			exactValue:      float64Ptr(0.0),
			description:     "Nil baseline with completion gives neutral score",
		},
		{
			name:            "nil baseline incomplete",
			baseline:        nil,
			tokens:          100,
			errors:          0,
			userCorrections: 0,
			completed:       false,
			exactValue:      float64Ptr(-1.0),
			description:     "Nil baseline without completion gives -1",
		},
		{
			name: "boundary cold start count 9",
			baseline: &TaskBaseline{
				Count: 9,
			},
			tokens:          100,
			errors:          0,
			userCorrections: 0,
			completed:       true,
			wantPositive:    true,
			description:     "Count=9 is still cold start",
		},
		{
			name: "boundary normal count 10",
			baseline: &TaskBaseline{
				Count:               10,
				MeanTokens:          1000,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            10000,
				M2Errors:            100,
				M2UserCorrections:   40,
			},
			tokens:          800,
			errors:          1,
			userCorrections: 0,
			completed:       true,
			wantPositive:    true,
			description:     "Count=10 switches to z-score mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeTaskScore(tt.baseline, tt.tokens, tt.errors, tt.userCorrections, tt.completed)

			if tt.exactValue != nil {
				if math.Abs(got-*tt.exactValue) > 1e-9 {
					t.Errorf("ComputeTaskScore() = %v, want exactly %v (%s)",
						got, *tt.exactValue, tt.description)
				}
				return
			}

			if tt.wantPositive && got <= 0 {
				t.Errorf("ComputeTaskScore() = %v, want positive (%s)", got, tt.description)
			}
			if !tt.wantPositive && got >= 0 {
				t.Errorf("ComputeTaskScore() = %v, want negative (%s)", got, tt.description)
			}
		})
	}
}

// TestUpdateBaseline verifies Welford's online algorithm correctness.
func TestUpdateBaseline(t *testing.T) {
	tests := []struct {
		name            string
		initial         *TaskBaseline
		tokens          int
		errors          int
		userCorrections int
		wantCount       int
		checkMean       bool
		wantMeanTokens  float64
		description     string
	}{
		{
			name:            "initialize new baseline",
			initial:         nil,
			tokens:          100,
			errors:          5,
			userCorrections: 2,
			wantCount:       1,
			checkMean:       true,
			wantMeanTokens:  100,
			description:     "Nil initial should create baseline with count=1",
		},
		{
			name: "update existing baseline",
			initial: &TaskBaseline{
				Count:               5,
				MeanTokens:          100,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            1000,
				M2Errors:            50,
				M2UserCorrections:   20,
			},
			tokens:          120,
			errors:          3,
			userCorrections: 1,
			wantCount:       6,
			checkMean:       true,
			wantMeanTokens:  103.333333, // (100*5 + 120) / 6 = 103.33...
			description:     "Count should increment and mean should update",
		},
		{
			name: "converging mean",
			initial: &TaskBaseline{
				Count:               10,
				MeanTokens:          100,
				MeanErrors:          5,
				MeanUserCorrections: 2,
				M2Tokens:            1000,
				M2Errors:            50,
				M2UserCorrections:   20,
			},
			tokens:          100, // same as mean
			errors:          5,
			userCorrections: 2,
			wantCount:       11,
			checkMean:       true,
			wantMeanTokens:  100, // mean unchanged when x == mean
			description:     "Adding value equal to mean should not change mean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpdateBaseline(tt.initial, tt.tokens, tt.errors, tt.userCorrections)

			if got.Count != tt.wantCount {
				t.Errorf("UpdateBaseline() count = %v, want %v (%s)",
					got.Count, tt.wantCount, tt.description)
			}

			if tt.checkMean {
				if math.Abs(got.MeanTokens-tt.wantMeanTokens) > 0.0001 {
					t.Errorf("UpdateBaseline() mean tokens = %v, want %v (%s)",
						got.MeanTokens, tt.wantMeanTokens, tt.description)
				}
			}

			// Ensure M2 is non-negative
			if got.M2Tokens < 0 || got.M2Errors < 0 || got.M2UserCorrections < 0 {
				t.Errorf("UpdateBaseline() M2 values should be non-negative, got M2Tokens=%v, M2Errors=%v, M2UserCorrections=%v",
					got.M2Tokens, got.M2Errors, got.M2UserCorrections)
			}
		})
	}
}

// TestWelfordAlgorithm validates Welford's algorithm produces correct statistics.
func TestWelfordAlgorithm(t *testing.T) {
	// Simulate adding values [10, 20, 30] and verify statistics
	values := []int{10, 20, 30}

	var baseline *TaskBaseline
	for _, v := range values {
		baseline = UpdateBaseline(baseline, v, 0, 0)
	}

	// Expected: mean = 20, variance = 100 (population) or 150 (sample)
	expectedMean := 20.0
	if math.Abs(baseline.MeanTokens-expectedMean) > 0.0001 {
		t.Errorf("Mean after 3 values = %v, want %v", baseline.MeanTokens, expectedMean)
	}

	// Sample standard deviation: sqrt(100) = 10
	expectedStdDev := 10.0
	gotStdDev := StdDev(baseline.M2Tokens, baseline.Count)
	if math.Abs(gotStdDev-expectedStdDev) > 0.0001 {
		t.Errorf("StdDev after 3 values = %v, want %v", gotStdDev, expectedStdDev)
	}
}

// TestStdDev verifies standard deviation calculation and cold start behavior.
func TestStdDev(t *testing.T) {
	tests := []struct {
		name        string
		m2          float64
		count       int
		want        float64
		description string
	}{
		{
			name:        "cold start count 0",
			m2:          100,
			count:       0,
			want:        1.0,
			description: "Count < 2 should return 1.0",
		},
		{
			name:        "cold start count 1",
			m2:          100,
			count:       1,
			want:        1.0,
			description: "Count < 2 should return 1.0",
		},
		{
			name:        "two observations",
			m2:          2.0, // (1-0)^2 + (1-0)^2 = 2? No, Welford gives different
			count:       2,
			want:        math.Sqrt(2.0), // sqrt(M2/(n-1)) = sqrt(2/1) = sqrt(2)
			description: "Two observations with M2=2",
		},
		{
			name:        "zero M2",
			m2:          0,
			count:       10,
			want:        0,
			description: "Zero variance gives zero std dev",
		},
		{
			name:        "large M2",
			m2:          900,
			count:       10,
			want:        math.Sqrt(100), // sqrt(900/9) = 10
			description: "Large M2 produces correct std dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StdDev(tt.m2, tt.count)
			if math.Abs(got-tt.want) > 0.0001 {
				t.Errorf("StdDev(%v, %v) = %v, want %v (%s)",
					tt.m2, tt.count, got, tt.want, tt.description)
			}
		})
	}
}

// TestStdDevNegativeM2 verifies StdDev handles numerical precision issues.
func TestStdDevNegativeM2(t *testing.T) {
	// Very small negative M2 due to floating point precision
	got := StdDev(-1e-15, 10)
	if got != 0 {
		t.Errorf("StdDev(-1e-15, 10) = %v, want 0 (should handle negative M2)", got)
	}
}

// TestRetrievedMemoryType verifies the RetrievedMemory type structure.
func TestRetrievedMemoryType(t *testing.T) {
	// Test with SelfReportScore set
	score := 2
	mem := RetrievedMemory{
		MemoryID:        ids.MustParse("018e1234-5678-7abc-8def-0123456789ab"),
		Similarity:      0.85,
		SelfReportScore: &score,
	}

	if mem.Similarity != 0.85 {
		t.Errorf("RetrievedMemory.Similarity = %v, want 0.85", mem.Similarity)
	}
	if mem.SelfReportScore == nil || *mem.SelfReportScore != 2 {
		t.Errorf("RetrievedMemory.SelfReportScore = %v, want 2", mem.SelfReportScore)
	}

	// Test with nil SelfReportScore
	mem2 := RetrievedMemory{
		MemoryID:        ids.MustParse("018e1234-5678-7abc-8def-0123456789ab"),
		Similarity:      0.5,
		SelfReportScore: nil,
	}
	if mem2.SelfReportScore != nil {
		t.Errorf("RetrievedMemory.SelfReportScore should be nil, got %v", *mem2.SelfReportScore)
	}
}

// BenchmarkUpdateWeight measures performance of weight updates.
func BenchmarkUpdateWeight(b *testing.B) {
	weight := 1.0
	credit := 2.0
	learningRate := 0.1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		weight = UpdateWeight(weight, credit, learningRate)
	}
}

// BenchmarkComputeCredit measures performance of credit calculation.
func BenchmarkComputeCredit(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ComputeCredit(2.0, 3, 5)
	}
}

// BenchmarkComputeTaskScoreColdStart measures cold start performance.
func BenchmarkComputeTaskScoreColdStart(b *testing.B) {
	baseline := &TaskBaseline{Count: 5}
	for i := 0; i < b.N; i++ {
		ComputeTaskScore(baseline, 100, 0, 0, true)
	}
}

// BenchmarkComputeTaskScoreZScore measures z-score performance.
func BenchmarkComputeTaskScoreZScore(b *testing.B) {
	baseline := &TaskBaseline{
		Count:               100,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            10000,
		M2Errors:            100,
		M2UserCorrections:   40,
	}
	for i := 0; i < b.N; i++ {
		ComputeTaskScore(baseline, 800, 1, 0, true)
	}
}

// BenchmarkUpdateBaseline measures baseline update performance.
func BenchmarkUpdateBaseline(b *testing.B) {
	baseline := &TaskBaseline{
		Count:               50,
		MeanTokens:          1000,
		MeanErrors:          5,
		MeanUserCorrections: 2,
		M2Tokens:            10000,
		M2Errors:            100,
		M2UserCorrections:   40,
	}
	for i := 0; i < b.N; i++ {
		baseline = UpdateBaseline(baseline, 950, 3, 1)
	}
}

// BenchmarkStdDev measures standard deviation performance.
func BenchmarkStdDev(b *testing.B) {
	for i := 0; i < b.N; i++ {
		StdDev(10000, 100)
	}
}

// Helper function to create float64 pointer
func float64Ptr(f float64) *float64 {
	return &f
}
