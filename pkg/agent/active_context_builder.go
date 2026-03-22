package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
)

const projectionImmutableHistoryLimit = 2048

type projectionBudget struct {
	Total     int
	System    int
	Recent    int
	DAG       int
	Retrieval int
}

func newProjectionBudget(maxTokens int) projectionBudget {
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	system := maxTokens * 35 / 100
	recent := maxTokens * 40 / 100
	dagBudget := maxTokens * 10 / 100
	retrieval := maxTokens - system - recent - dagBudget
	if retrieval < 0 {
		retrieval = 0
	}

	return projectionBudget{
		Total:     maxTokens,
		System:    system,
		Recent:    recent,
		DAG:       dagBudget,
		Retrieval: retrieval,
	}
}

type TurnContextBuildRequest struct {
	ProjectionRequest memory.ProjectionRequest
	CurrentMessage    string
	NoHistory         bool
	FallbackHistory   []messages.Message
	Summary           string
}

type TurnContextBuild struct {
	Projection *memory.ActiveContextProjection
	History    []messages.Message
}

// DefaultActiveContextBuilder materializes the contract-defined projection
// from the runtime's existing system prompt builder, immutable history, DAG
// summaries, and hybrid memory retrieval.
type DefaultActiveContextBuilder struct {
	agentID        string
	contextBuilder *ContextBuilder
	sessions       sessionSummaryReader
	memDelegate    memory.MemoryDelegate
	memoryStore    *memstore.MemoryStore
	queries        *memsqlc.Queries
}

type sessionSummaryReader interface {
	GetSummary(key string) string
}

var _ memory.ActiveContextBuilder = (*DefaultActiveContextBuilder)(nil)

func NewDefaultActiveContextBuilder(agentID string, contextBuilder *ContextBuilder, sessions sessionSummaryReader, memDelegate memory.MemoryDelegate, memoryStore *memstore.MemoryStore, queries *memsqlc.Queries) *DefaultActiveContextBuilder {
	return &DefaultActiveContextBuilder{
		agentID:        agentID,
		contextBuilder: contextBuilder,
		sessions:       sessions,
		memDelegate:    memDelegate,
		memoryStore:    memoryStore,
		queries:        queries,
	}
}

func (b *DefaultActiveContextBuilder) BuildActiveContext(ctx context.Context, req memory.ProjectionRequest) (*memory.ActiveContextProjection, error) {
	built, err := b.BuildTurnContext(ctx, TurnContextBuildRequest{
		ProjectionRequest: req,
	})
	if err != nil {
		return nil, err
	}
	return built.Projection, nil
}

func (b *DefaultActiveContextBuilder) BuildTurnContext(ctx context.Context, req TurnContextBuildRequest) (*TurnContextBuild, error) {
	agentID := strings.TrimSpace(req.ProjectionRequest.AgentID)
	if agentID == "" {
		agentID = b.agentID
	}

	budget := newProjectionBudget(req.ProjectionRequest.MaxTokens)
	now := time.Now().UTC()
	projection := &memory.ActiveContextProjection{
		AgentID:       agentID,
		SessionKey:    req.ProjectionRequest.SessionKey,
		BudgetTokens:  budget.Total,
		GeneratedAt:   now,
		ProjectionRef: fmt.Sprintf("%s:%d", req.ProjectionRequest.SessionKey, now.UnixNano()),
	}

	summary := strings.TrimSpace(req.Summary)
	if summary == "" && b.sessions != nil {
		summary = strings.TrimSpace(b.sessions.GetSummary(req.ProjectionRequest.SessionKey))
	}

	systemSegments := b.buildSystemSegments(req.ProjectionRequest.SessionKey, summary, budget.System)
	projection.Segments = append(projection.Segments, systemSegments...)

	var immutableHistory []*memory.ImmutableMessage
	if !req.NoHistory && b.memDelegate != nil {
		historyRows, err := b.memDelegate.ListImmutableMessages(ctx, req.ProjectionRequest.SessionKey, projectionImmutableHistoryLimit, 0)
		if err != nil {
			logger.WarnCF("context", "Active context builder failed to load immutable history",
				map[string]interface{}{
					"session_key": req.ProjectionRequest.SessionKey,
					"error":       err.Error(),
				})
		} else {
			immutableHistory = historyRows
		}
	}

	history, historySegments := b.buildHistorySegments(req.ProjectionRequest.SessionKey, immutableHistory, req.FallbackHistory, budget.Recent)
	projection.Segments = append(projection.Segments, historySegments...)

	if !req.NoHistory {
		projection.Segments = append(projection.Segments, b.buildDAGSegments(ctx, req.ProjectionRequest.SessionKey, immutableHistory, budget.DAG)...)
		projection.Segments = append(projection.Segments, b.buildRetrievalSegments(ctx, req.ProjectionRequest.SessionKey, req.CurrentMessage, budget.Retrieval)...)
	}

	return &TurnContextBuild{
		Projection: projection,
		History:    history,
	}, nil
}

func (b *DefaultActiveContextBuilder) buildSystemSegments(sessionKey, summary string, budget int) []memory.ProjectionSegment {
	if b.contextBuilder == nil || budget <= 0 {
		return nil
	}

	candidates := make([]memory.ProjectionSegment, 0, 2)

	systemPrompt := strings.TrimSpace(b.contextBuilder.BuildSystemPromptWithBudget(0))
	if systemPrompt != "" {
		candidates = append(candidates, memory.ProjectionSegment{
			Kind:   memory.ProjectionSegmentSystem,
			Source: "runtime_system",
			Text:   systemPrompt,
			Tokens: observation.EstimateTokens(systemPrompt),
			Ref:    zeroSpanRef(sessionKey),
		})
	}

	if summary != "" {
		summaryText := "## Summary of Previous Conversation\n\n" + summary
		candidates = append(candidates, memory.ProjectionSegment{
			Kind:   memory.ProjectionSegmentSystem,
			Source: "session_summary",
			Text:   summaryText,
			Tokens: observation.EstimateTokens(summaryText),
			Ref:    zeroSpanRef(sessionKey),
		})
	}

	return fitSegmentsToBudget(candidates, budget)
}

func (b *DefaultActiveContextBuilder) buildHistorySegments(sessionKey string, immutableHistory []*memory.ImmutableMessage, fallbackHistory []messages.Message, budget int) ([]messages.Message, []memory.ProjectionSegment) {
	if budget <= 0 {
		return nil, nil
	}

	if len(immutableHistory) > 0 {
		return buildHistoryFromImmutable(sessionKey, immutableHistory, budget)
	}
	return buildHistoryFromFallback(sessionKey, fallbackHistory, budget)
}

func buildHistoryFromImmutable(sessionKey string, immutableHistory []*memory.ImmutableMessage, budget int) ([]messages.Message, []memory.ProjectionSegment) {
	if len(immutableHistory) == 0 {
		return nil, nil
	}

	minTail := dag.TailMessageCount(budget)
	selected := make([]int, 0, minTail)
	remaining := budget

	for idx := len(immutableHistory) - 1; idx >= 0; idx-- {
		msg := immutableHistory[idx]
		tokens := msg.TokenEstimate
		if tokens <= 0 {
			tokens = observation.EstimateTokens(renderImmutableMessageText(msg))
		}

		if len(selected) >= minTail && remaining-tokens < 0 {
			break
		}

		selected = append(selected, idx)
		remaining -= tokens
	}

	reverseInts(selected)

	history := make([]messages.Message, 0, len(selected))
	segments := make([]memory.ProjectionSegment, 0, len(selected))
	for _, idx := range selected {
		msg := immutableHistory[idx]
		sessionMsg := immutableToSessionMessage(msg)
		history = append(history, sessionMsg)

		text := renderSessionMessageText(sessionMsg)
		kind := memory.ProjectionSegmentRecent
		if msg.Role == "tool" {
			kind = memory.ProjectionSegmentTool
		}

		segments = append(segments, memory.ProjectionSegment{
			Kind:   kind,
			Source: "immutable:" + msg.Role,
			Text:   text,
			Tokens: max(msg.TokenEstimate, observation.EstimateTokens(text)),
			Ref:    immutableMessageRef(sessionKey, idx, msg),
		})
	}

	return history, segments
}

func buildHistoryFromFallback(sessionKey string, fallbackHistory []messages.Message, budget int) ([]messages.Message, []memory.ProjectionSegment) {
	if len(fallbackHistory) == 0 {
		return nil, nil
	}

	minTail := dag.TailMessageCount(budget)
	selected := make([]int, 0, minTail)
	remaining := budget

	for idx := len(fallbackHistory) - 1; idx >= 0; idx-- {
		msg := fallbackHistory[idx]
		tokens := observation.EstimateTokens(renderSessionMessageText(msg))
		if len(selected) >= minTail && remaining-tokens < 0 {
			break
		}

		selected = append(selected, idx)
		remaining -= tokens
	}

	reverseInts(selected)

	history := make([]messages.Message, 0, len(selected))
	segments := make([]memory.ProjectionSegment, 0, len(selected))
	for _, idx := range selected {
		msg := fallbackHistory[idx]
		history = append(history, msg)

		kind := memory.ProjectionSegmentRecent
		if msg.Role == "tool" {
			kind = memory.ProjectionSegmentTool
		}

		segments = append(segments, memory.ProjectionSegment{
			Kind:   kind,
			Source: "session_cache:" + msg.Role,
			Text:   renderSessionMessageText(msg),
			Tokens: observation.EstimateTokens(renderSessionMessageText(msg)),
			Ref: memory.ImmutableSpanRef{
				SessionKey: sessionKey,
				StartIdx:   idx,
				EndIdx:     idx + 1,
			},
		})
	}

	return history, segments
}

func (b *DefaultActiveContextBuilder) buildDAGSegments(ctx context.Context, sessionKey string, immutableHistory []*memory.ImmutableMessage, budget int) []memory.ProjectionSegment {
	if budget <= 0 || b.queries == nil {
		return nil
	}

	snapshot, err := b.queries.GetLatestDAGSnapshotBySession(ctx, memsqlc.GetLatestDAGSnapshotBySessionParams{
		AgentID:    b.agentID,
		SessionKey: sessionKey,
	})
	if err != nil {
		return nil
	}

	nodes, err := b.queries.ListDAGNodesBySnapshotID(ctx, memsqlc.ListDAGNodesBySnapshotIDParams{
		SnapshotID: snapshot.ID,
	})
	if err != nil || len(nodes) == 0 {
		return nil
	}

	selected := selectDAGNodesForBudget(nodes, budget)
	segments := make([]memory.ProjectionSegment, 0, len(selected))
	for _, node := range selected {
		text := fmt.Sprintf("[%d-%d] %s", node.StartIdx, max(node.EndIdx-1, node.StartIdx), strings.TrimSpace(node.Summary))
		segments = append(segments, memory.ProjectionSegment{
			Kind:   memory.ProjectionSegmentDAG,
			Source: fmt.Sprintf("dag:%s:%s", sessionKey, node.NodeID),
			Text:   text,
			Tokens: max(int(node.Tokens), observation.EstimateTokens(text)),
			Ref:    dagNodeRef(sessionKey, immutableHistory, node),
		})
	}

	return fitSegmentsToBudget(segments, budget)
}

func (b *DefaultActiveContextBuilder) buildRetrievalSegments(ctx context.Context, sessionKey, currentMessage string, budget int) []memory.ProjectionSegment {
	if budget <= 0 || b.memoryStore == nil || strings.TrimSpace(currentMessage) == "" {
		return nil
	}

	results, err := b.memoryStore.Search(ctx, currentMessage, memory.SearchOptions{
		AgentID:    b.agentID,
		SessionKey: sessionKey,
		Limit:      8,
	})
	if err != nil {
		logger.WarnCF("context", "Active context builder retrieval search failed",
			map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		return nil
	}

	segments := make([]memory.ProjectionSegment, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Content) == "" {
			continue
		}
		if strings.HasPrefix(result.Source, "working-context:") || strings.HasPrefix(result.Source, "dag:") {
			continue
		}

		kind := memory.ProjectionSegmentArchival
		if result.Source == sessionKey {
			kind = memory.ProjectionSegmentRecall
		}

		key := string(kind) + ":" + result.ID.String() + ":" + result.Source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		segments = append(segments, memory.ProjectionSegment{
			Kind:   kind,
			Source: result.Source,
			Text:   strings.TrimSpace(result.Content),
			Tokens: observation.EstimateTokens(result.Content),
			Ref:    zeroSpanRef(sessionKey),
		})
	}

	return fitSegmentsToBudget(segments, budget)
}

func selectDAGNodesForBudget(nodes []memsqlc.DagNode, budget int) []memsqlc.DagNode {
	if budget <= 0 || len(nodes) == 0 {
		return nil
	}

	grouped := map[int64][]memsqlc.DagNode{}
	totals := map[int64]int{}
	for _, node := range nodes {
		grouped[node.Level] = append(grouped[node.Level], node)
		totals[node.Level] += max(int(node.Tokens), observation.EstimateTokens(node.Summary))
	}

	for _, level := range []int64{int64(dag.LevelSession), int64(dag.LevelSection), int64(dag.LevelChunk)} {
		if len(grouped[level]) == 0 {
			continue
		}
		if totals[level] <= budget {
			return grouped[level]
		}
	}

	for _, level := range []int64{int64(dag.LevelSession), int64(dag.LevelSection), int64(dag.LevelChunk)} {
		if len(grouped[level]) > 0 {
			return grouped[level]
		}
	}

	return nil
}

func fitSegmentsToBudget(segments []memory.ProjectionSegment, budget int) []memory.ProjectionSegment {
	if budget <= 0 || len(segments) == 0 {
		return nil
	}

	out := make([]memory.ProjectionSegment, 0, len(segments))
	remaining := budget
	for _, seg := range segments {
		if strings.TrimSpace(seg.Text) == "" {
			continue
		}

		if seg.Tokens <= 0 {
			seg.Tokens = observation.EstimateTokens(seg.Text)
		}

		if seg.Tokens <= remaining {
			out = append(out, seg)
			remaining -= seg.Tokens
			continue
		}

		if remaining < 32 {
			break
		}

		seg.Text = truncateToTokenBudget(seg.Text, remaining)
		seg.Tokens = observation.EstimateTokens(seg.Text)
		out = append(out, seg)
		break
	}

	return out
}

func immutableToSessionMessage(msg *memory.ImmutableMessage) messages.Message {
	if msg == nil {
		return messages.Message{}
	}

	sessionMsg := messages.Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
	}
	if strings.TrimSpace(msg.ToolCalls) != "" {
		_ = jsonv2.Unmarshal([]byte(msg.ToolCalls), &sessionMsg.ToolCalls)
	}
	return sessionMsg
}

func renderImmutableMessageText(msg *memory.ImmutableMessage) string {
	if msg == nil {
		return ""
	}
	return renderSessionMessageText(immutableToSessionMessage(msg))
}

func renderSessionMessageText(msg messages.Message) string {
	content := strings.TrimSpace(msg.Content)
	if content != "" {
		return content
	}

	if len(msg.ToolCalls) == 0 {
		return ""
	}

	parts := make([]string, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if call.Function != nil && strings.TrimSpace(call.Function.Name) != "" {
			name = strings.TrimSpace(call.Function.Name)
		}
		if name == "" {
			name = "tool"
		}
		parts = append(parts, name)
	}

	return "Tool calls: " + strings.Join(parts, ", ")
}

func zeroSpanRef(sessionKey string) memory.ImmutableSpanRef {
	return memory.ImmutableSpanRef{SessionKey: sessionKey}
}

func immutableMessageRef(sessionKey string, idx int, msg *memory.ImmutableMessage) memory.ImmutableSpanRef {
	ref := memory.ImmutableSpanRef{
		SessionKey: sessionKey,
		StartIdx:   idx,
		EndIdx:     idx + 1,
	}
	if msg == nil {
		return ref
	}
	ref.FirstID = msg.ID
	ref.LastID = msg.ID
	ref.FromTime = msg.CreatedAt
	ref.ToTime = msg.CreatedAt
	return ref
}

func dagNodeRef(sessionKey string, immutableHistory []*memory.ImmutableMessage, node memsqlc.DagNode) memory.ImmutableSpanRef {
	ref := memory.ImmutableSpanRef{
		SessionKey: sessionKey,
		StartIdx:   max(0, int(node.StartIdx)),
		EndIdx:     max(0, int(node.EndIdx)),
	}

	if len(immutableHistory) == 0 {
		if ref.EndIdx < ref.StartIdx {
			ref.EndIdx = ref.StartIdx
		}
		return ref
	}

	if ref.StartIdx > len(immutableHistory) {
		ref.StartIdx = len(immutableHistory)
	}
	if ref.EndIdx > len(immutableHistory) {
		ref.EndIdx = len(immutableHistory)
	}
	if ref.EndIdx < ref.StartIdx {
		ref.EndIdx = ref.StartIdx
	}
	if ref.EndIdx == ref.StartIdx {
		return ref
	}

	first := immutableHistory[ref.StartIdx]
	last := immutableHistory[ref.EndIdx-1]
	ref.FirstID = first.ID
	ref.LastID = last.ID
	ref.FromTime = first.CreatedAt
	ref.ToTime = last.CreatedAt
	return ref
}

func reverseInts(values []int) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
