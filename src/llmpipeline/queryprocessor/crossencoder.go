package queryprocessor

import "sort"

type CrossEncoder struct{}

type CrossEncoderProvider interface {
	RankResults(results []string) []RankedResult
}

type RankedResult struct {
	Result string
	Score  float64
}

func NewCrossEncoder() CrossEncoder {
	return CrossEncoder{}
}

func (ce CrossEncoder) RankResults(results []string) []RankedResult {
	rankedResults := make([]RankedResult, len(results))

	// FIXME: Implement a proper cross encoder ranking algorithm
	for i, result := range results {
		rankedResults[i] = RankedResult{
			Result: result,
			Score:  float64(len(result)),
		}
	}
	sort.Slice(rankedResults, func(i, j int) bool {
		return rankedResults[i].Score > rankedResults[j].Score
	})
	return rankedResults
}
