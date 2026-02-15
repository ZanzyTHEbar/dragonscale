package store

import (
	"math"
	"sort"
	"time"

	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory"
)

// CosineSimilarity computes the cosine similarity between two embedding vectors.
// Returns 0 if either vector is zero-length or has zero norm.
// Uses manual dot product and L2 norm to avoid gonum's float64-only API.
func CosineSimilarity(a, b memory.Embedding) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	normA = math.Sqrt(normA)
	normB = math.Sqrt(normB)
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (normA * normB)
}

// VectorSearchInput pairs an archival chunk with its embedding for search.
type VectorSearchInput struct {
	Chunk     *memory.ArchivalChunk
	Embedding memory.Embedding
}

// VectorSearch performs brute-force cosine similarity search, returning top-k results.
// This is the Go-side fallback when DB-side vector_top_k() is unavailable.
func VectorSearch(queryVec memory.Embedding, items []VectorSearchInput, limit int) []memory.SearchResult {
	if len(queryVec) == 0 || len(items) == 0 || limit <= 0 {
		return nil
	}

	type scored struct {
		input VectorSearchInput
		score float64
	}

	results := make([]scored, 0, len(items))
	for _, item := range items {
		if len(item.Embedding) == 0 {
			continue
		}
		sim := CosineSimilarity(queryVec, item.Embedding)
		if math.IsNaN(sim) || math.IsInf(sim, 0) {
			continue
		}
		results = append(results, scored{input: item, score: sim})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if limit > len(results) {
		limit = len(results)
	}

	out := make([]memory.SearchResult, limit)
	for i := 0; i < limit; i++ {
		r := results[i]
		out[i] = memory.SearchResult{
			ID:      r.input.Chunk.ID,
			Content: r.input.Chunk.Content,
			Source:  r.input.Chunk.Source,
			Score:   r.score,
		}
	}
	return out
}

// ReciprocalRankFusion merges multiple ranked result lists using RRF.
// Each result set should be sorted by relevance (best first).
// weights[i] scales the contribution of resultSets[i]. k is the fusion constant (default 60).
func ReciprocalRankFusion(resultSets [][]memory.SearchResult, weights []float64, k float64) []memory.SearchResult {
	if len(resultSets) == 0 {
		return nil
	}
	if k <= 0 {
		k = 60
	}

	type rrfEntry struct {
		result memory.SearchResult
		score  float64
	}
	scores := make(map[ids.UUID]*rrfEntry)

	for setIdx, results := range resultSets {
		w := 1.0
		if setIdx < len(weights) {
			w = weights[setIdx]
		}
		for rank, r := range results {
			rrf := w / (k + float64(rank+1))
			if existing, ok := scores[r.ID]; ok {
				existing.score += rrf
			} else {
				scores[r.ID] = &rrfEntry{result: r, score: rrf}
			}
		}
	}

	merged := make([]memory.SearchResult, 0, len(scores))
	for _, e := range scores {
		e.result.Score = e.score
		merged = append(merged, e.result)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}

// RecencyDecay computes an exponential decay multiplier based on age.
// halfLifeHours controls how fast the score decays. Returns (0, 1].
func RecencyDecay(age time.Duration, halfLifeHours float64) float64 {
	if halfLifeHours <= 0 {
		return 1.0
	}
	hours := age.Hours()
	if hours <= 0 {
		return 1.0
	}
	return math.Pow(0.5, hours/halfLifeHours)
}

// ApplyRecencyDecay multiplies each result's score by a recency decay factor.
func ApplyRecencyDecay(results []memory.SearchResult, now time.Time, halfLifeHours float64, createdAtFn func(id ids.UUID) time.Time) {
	if halfLifeHours <= 0 || createdAtFn == nil {
		return
	}
	for i := range results {
		created := createdAtFn(results[i].ID)
		if created.IsZero() {
			continue
		}
		decay := RecencyDecay(now.Sub(created), halfLifeHours)
		results[i].Score *= decay
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}
