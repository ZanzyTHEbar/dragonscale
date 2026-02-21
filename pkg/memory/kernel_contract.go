package memory

import (
	"context"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
)

// ProjectionSegmentKind classifies what role a segment plays in active context.
type ProjectionSegmentKind string

const (
	ProjectionSegmentSystem   ProjectionSegmentKind = "system"
	ProjectionSegmentRecent   ProjectionSegmentKind = "recent"
	ProjectionSegmentDAG      ProjectionSegmentKind = "dag"
	ProjectionSegmentRecall   ProjectionSegmentKind = "recall"
	ProjectionSegmentArchival ProjectionSegmentKind = "archival"
	ProjectionSegmentTool     ProjectionSegmentKind = "tool_result"
)

// ImmutableSpanRef is a lossless reference back into immutable session history.
type ImmutableSpanRef struct {
	SessionKey string    `json:"session_key"`
	StartIdx   int       `json:"start_idx"` // inclusive
	EndIdx     int       `json:"end_idx"`   // exclusive
	FirstID    ids.UUID  `json:"first_id"`
	LastID     ids.UUID  `json:"last_id"`
	FromTime   time.Time `json:"from_time"`
	ToTime     time.Time `json:"to_time"`
}

// ProjectionSegment is one unit included in active context.
// Every segment must carry a lossless reference back to immutable history.
type ProjectionSegment struct {
	Kind   ProjectionSegmentKind `json:"kind"`
	Source string                `json:"source"`
	Text   string                `json:"text"`
	Tokens int                   `json:"tokens"`
	Ref    ImmutableSpanRef      `json:"ref"`
}

// ActiveContextProjection is the assembled view injected into the model for a turn.
// It is a materialized projection, not a source of truth.
type ActiveContextProjection struct {
	AgentID       string              `json:"agent_id"`
	SessionKey    string              `json:"session_key"`
	BudgetTokens  int                 `json:"budget_tokens"`
	GeneratedAt   time.Time           `json:"generated_at"`
	ProjectionRef string              `json:"projection_ref"`
	Segments      []ProjectionSegment `json:"segments"`
}

func (p *ActiveContextProjection) TotalTokens() int {
	if p == nil {
		return 0
	}
	total := 0
	for _, seg := range p.Segments {
		total += seg.Tokens
	}
	return total
}

// HasLosslessRefs verifies the projection preserves deterministic pointers to
// immutable history for every segment.
func (p *ActiveContextProjection) HasLosslessRefs() bool {
	if p == nil {
		return false
	}
	for _, seg := range p.Segments {
		if seg.Ref.SessionKey == "" || seg.Ref.EndIdx < seg.Ref.StartIdx {
			return false
		}
	}
	return true
}

type ProjectionRequest struct {
	AgentID      string
	SessionKey   string
	MaxTokens    int
	IncludeTools bool
}

// ImmutableHistoryReader resolves lossless references to original messages.
type ImmutableHistoryReader interface {
	ListRecallItems(ctx context.Context, agentID, sessionKey string, limit, offset int) ([]*RecallItem, error)
}

// ActiveContextBuilder materializes the turn-time projection from immutable
// storage, retrieval tiers, and DAG summaries.
type ActiveContextBuilder interface {
	BuildActiveContext(ctx context.Context, req ProjectionRequest) (*ActiveContextProjection, error)
}
