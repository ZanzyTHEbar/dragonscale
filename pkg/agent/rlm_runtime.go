package agent

import (
	"context"
	"fmt"
	"strings"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	"github.com/ZanzyTHEbar/dragonscale/pkg/rlm"
)

type rlmAnswerer interface {
	Answer(ctx context.Context, sessionKey, query, context_ string) (string, uint32, error)
}

const rlmReducerSource = "rlm_context_reducer"

func newLiveRLMAnswerer(model fantasy.LanguageModel) (rlmAnswerer, int) {
	cfg := rlm.DefaultEngineConfig()
	engine := rlm.NewEngine(cfg, nil, makeRLMCallModel(model))
	return engine, cfg.Strategy.DirectThreshold
}

func makeRLMCallModel(model fantasy.LanguageModel) rlm.CallModelFunc {
	return func(ctx context.Context, ctxContent, query string) (string, uint32, error) {
		systemPrompt := "You reduce large runtime context for an autonomous agent. Extract only the facts, constraints, prior decisions, and tool outcomes that are relevant to the user query. Return a compact context note, not a final assistant reply."
		userPrompt := fmt.Sprintf("Query:\n%s\n\nContext:\n%s", query, ctxContent)
		if strings.HasPrefix(ctxContent, "You are synthesising answers from multiple text partitions.") {
			systemPrompt = ctxContent
			userPrompt = query
		}

		maxTokens := int64(384)
		resp, err := model.Generate(ctx, fantasy.Call{
			Prompt: fantasy.Prompt{
				fantasy.NewSystemMessage(systemPrompt),
				fantasy.NewUserMessage(userPrompt),
			},
			MaxOutputTokens: &maxTokens,
		})
		if err != nil {
			return "", 0, err
		}
		return strings.TrimSpace(resp.Content.Text()), uint32(resp.Usage.TotalTokens), nil
	}
}

func (al *AgentLoop) maybeReduceProjectionWithRLM(ctx context.Context, sessionKey, query string, projection *memory.ActiveContextProjection) *memory.ActiveContextProjection {
	if al == nil || al.rlmEngine == nil || projection == nil {
		return projection
	}

	candidateSegments := make([]memory.ProjectionSegment, 0, len(projection.Segments))
	for _, seg := range projection.Segments {
		if isRLMReducibleSegment(seg.Kind) {
			candidateSegments = append(candidateSegments, seg)
		}
	}
	if len(candidateSegments) == 0 {
		return projection
	}

	contextBlob := renderProjectionSegmentsForRLM(candidateSegments)
	if strings.TrimSpace(contextBlob) == "" || len(contextBlob) < al.rlmDirectThresholdBytes {
		return projection
	}

	reduced, _, err := al.rlmEngine.Answer(ctx, sessionKey, query, contextBlob)
	if err != nil {
		logger.WarnCF("agent", "RLM context reduction failed", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return projection
	}
	reduced = strings.TrimSpace(reduced)
	if reduced == "" {
		return projection
	}

	rlmSegment := memory.ProjectionSegment{
		Kind:   memory.ProjectionSegmentSystem,
		Source: rlmReducerSource,
		Text:   "## Recursive Context Reduction\n\n" + reduced,
		Tokens: observation.EstimateTokens(reduced),
		Ref:    mergeProjectionRefs(sessionKey, candidateSegments),
	}

	updated := make([]memory.ProjectionSegment, 0, len(projection.Segments)-len(candidateSegments)+1)
	inserted := false
	for _, seg := range projection.Segments {
		if isRLMReducibleSegment(seg.Kind) {
			if !inserted {
				updated = append(updated, rlmSegment)
				inserted = true
			}
			continue
		}
		updated = append(updated, seg)
	}

	clone := *projection
	clone.Segments = updated
	return &clone
}

func isRLMReducibleSegment(kind memory.ProjectionSegmentKind) bool {
	switch kind {
	case memory.ProjectionSegmentDAG, memory.ProjectionSegmentRecall, memory.ProjectionSegmentArchival:
		return true
	default:
		return false
	}
}

func renderProjectionSegmentsForRLM(segments []memory.ProjectionSegment) string {
	var sb strings.Builder
	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		sb.WriteString("### ")
		sb.WriteString(string(seg.Kind))
		if seg.Source != "" {
			sb.WriteString(" / ")
			sb.WriteString(seg.Source)
		}
		sb.WriteString("\n")
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

func mergeProjectionRefs(sessionKey string, segments []memory.ProjectionSegment) memory.ImmutableSpanRef {
	var merged memory.ImmutableSpanRef
	for _, seg := range segments {
		ref := seg.Ref
		if ref.SessionKey == "" {
			continue
		}
		if merged.SessionKey == "" {
			merged = ref
			continue
		}
		if ref.StartIdx < merged.StartIdx {
			merged.StartIdx = ref.StartIdx
			merged.FirstID = ref.FirstID
		}
		if merged.FromTime.IsZero() || (!ref.FromTime.IsZero() && ref.FromTime.Before(merged.FromTime)) {
			merged.FromTime = ref.FromTime
		}
		if ref.EndIdx > merged.EndIdx {
			merged.EndIdx = ref.EndIdx
			merged.LastID = ref.LastID
		}
		if merged.ToTime.IsZero() || ref.ToTime.After(merged.ToTime) {
			merged.ToTime = ref.ToTime
		}
	}
	if merged.SessionKey == "" {
		return memory.ImmutableSpanRef{SessionKey: sessionKey}
	}
	return merged
}
