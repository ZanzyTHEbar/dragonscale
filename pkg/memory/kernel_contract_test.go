package memory

import (
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/stretchr/testify/assert"
)

func TestActiveContextProjection_TotalTokens(t *testing.T) {
	p := &ActiveContextProjection{
		Segments: []ProjectionSegment{
			{Tokens: 120},
			{Tokens: 80},
			{Tokens: 35},
		},
	}
	assert.Equal(t, 235, p.TotalTokens())
}

func TestActiveContextProjection_HasLosslessRefs(t *testing.T) {
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
