package memory

import (
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestActiveContextProjection_TotalTokens(t *testing.T) {
	t.Parallel()
	p := &ActiveContextProjection{
		Segments: []ProjectionSegment{
			{Tokens: 120},
			{Tokens: 80},
			{Tokens: 35},
		},
	}
	assert.Empty(t, cmp.Diff(235, p.TotalTokens()))
}

func TestActiveContextProjection_HasLosslessRefs(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	p := &ActiveContextProjection{
		Segments: []ProjectionSegment{
			{
				Kind:   ProjectionSegmentRecent,
				Source: "session-tail",
				Text:   "latest messages",
				Tokens: 64,
				Ref: ImmutableSpanRef{
					SessionKey: "s1",
					StartIdx:   10,
					EndIdx:     15,
					FirstID:    ids.New(),
					LastID:     ids.New(),
					FromTime:   now,
					ToTime:     now,
				},
			},
		},
	}
	assert.True(t, p.HasLosslessRefs())

	p.Segments[0].Ref.SessionKey = ""
	assert.False(t, p.HasLosslessRefs())
}
