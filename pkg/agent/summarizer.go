package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	"github.com/ZanzyTHEbar/dragonscale/pkg/contexttree"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
)

// maybeSummarize implements LCM ADR-002 dual-threshold compaction.
// Below soft threshold: no-op (zero cost).
// Between soft and hard: async background compaction.
// Above hard: synchronous blocking compaction with 3-level escalation.
func (al *AgentLoop) maybeSummarize(ctx context.Context, sessionKey, channel, chatID string) {
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)

	softPct, hardPct := al.compactionThresholds()
	softThreshold := al.contextWindow * softPct / 100
	hardThreshold := al.contextWindow * hardPct / 100

	if tokenEstimate <= softThreshold {
		return
	}

	if tokenEstimate > hardThreshold {
		al.forceCompression(ctx, sessionKey, channel, chatID)
		return
	}

	go func() {
		al.summarizeSession(context.WithoutCancel(ctx), sessionKey)
	}()
}

// compactionThresholds returns the soft and hard compaction percentages from config.
func (al *AgentLoop) compactionThresholds() (softPct, hardPct int) {
	softPct = 70
	hardPct = 90
	if al.cfg != nil {
		c := al.cfg.Agents.Defaults.Compaction
		if c.SoftThresholdPct > 0 && c.SoftThresholdPct < 100 {
			softPct = c.SoftThresholdPct
		}
		if c.HardThresholdPct > 0 && c.HardThresholdPct <= 100 {
			hardPct = c.HardThresholdPct
		}
	}
	if hardPct <= softPct {
		hardPct = softPct + 10
	}
	return softPct, hardPct
}

// EmergencyProvenance captures provenance metadata for postmortem when
// emergency compression cycles run. Persisted via the audit pipeline.
type EmergencyProvenance struct {
	SessionKey      string `json:"session_key"`
	Cycle           int    `json:"cycle"`
	TokenEstimate   int    `json:"token_estimate"`
	CriticalBudget  int    `json:"critical_budget"`
	HistoryMsgCount int    `json:"history_msg_count"`
}

// persistEmergencyProvenance writes provenance metadata to the audit log.
// Best-effort: logs warning on failure, never fails the compression path.
func (al *AgentLoop) persistEmergencyProvenance(ctx context.Context, prov EmergencyProvenance) {
	if al.memDelegate == nil {
		return
	}
	input, err := json.Marshal(prov)
	if err != nil {
		logger.WarnCF("agent", "Failed to marshal emergency provenance",
			map[string]interface{}{"error": err.Error()})
		return
	}
	entry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    pkg.NAME,
		SessionKey: prov.SessionKey,
		Action:     "emergency_compression",
		Target:     fmt.Sprintf("cycle_%d", prov.Cycle),
		Input:      string(input),
	}
	aCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := al.memDelegate.InsertAuditEntry(aCtx, entry); err != nil {
		logger.WarnCF("agent", "Failed to persist emergency provenance",
			map[string]interface{}{"error": err.Error(), "session_key": prov.SessionKey})
	}
}

// forceCompression performs emergency recursive compression by repeatedly
// summarizing older history until under hard budget, without deleting immutable
// persisted session records.
func (al *AgentLoop) forceCompression(ctx context.Context, sessionKey, channel, chatID string) {
	if _, loading := al.summarizing.LoadOrStore(sessionKey, true); loading {
		return
	}
	defer al.summarizing.Delete(sessionKey)

	if channel != "" && !constants.IsInternalChannel(channel) {
		if al.bus != nil {
			al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: "⚠️ Memory threshold reached. Optimizing conversation history...",
			})
		}
	}

	const maxCycles = 3
	for cycle := 1; cycle <= maxCycles; cycle++ {
		history := al.sessions.GetHistory(sessionKey)
		if len(history) <= al.continuityKeepCount(history) {
			return
		}
		tokenEstimate := al.estimateTokens(history)
		criticalThreshold := al.contextWindow * 95 / 100
		if tokenEstimate <= criticalThreshold {
			return
		}

		logger.WarnCF("agent", "Emergency compression cycle triggered",
			map[string]interface{}{
				"session_key":     sessionKey,
				"cycle":           cycle,
				"token_estimate":  tokenEstimate,
				"critical_budget": criticalThreshold,
			})

		al.persistEmergencyProvenance(ctx, EmergencyProvenance{
			SessionKey:      sessionKey,
			Cycle:           cycle,
			TokenEstimate:   tokenEstimate,
			CriticalBudget:  criticalThreshold,
			HistoryMsgCount: len(history),
		})

		al.summarizeSession(ctx, sessionKey, cycle)
	}
}

func (al *AgentLoop) continuityRetentionPolicy() config.ContinuityRetentionConfig {
	policy := config.ContinuityRetentionConfig{
		MinMessages:         4,
		MaxMessages:         24,
		TargetContextRatio:  0.10,
		FailureKeepMessages: 10,
	}

	if al.cfg != nil {
		cfgPolicy := al.cfg.Agents.Defaults.ContinuityRetention
		if cfgPolicy.MinMessages > 0 {
			policy.MinMessages = cfgPolicy.MinMessages
		}
		if cfgPolicy.MaxMessages > 0 {
			policy.MaxMessages = cfgPolicy.MaxMessages
		}
		if cfgPolicy.TargetContextRatio > 0 && cfgPolicy.TargetContextRatio <= 0.5 {
			policy.TargetContextRatio = cfgPolicy.TargetContextRatio
		}
		if cfgPolicy.FailureKeepMessages > 0 {
			policy.FailureKeepMessages = cfgPolicy.FailureKeepMessages
		}
	}

	if policy.MaxMessages < policy.MinMessages {
		policy.MaxMessages = policy.MinMessages
	}
	if policy.FailureKeepMessages < policy.MinMessages {
		policy.FailureKeepMessages = policy.MinMessages
	}

	return policy
}

func (al *AgentLoop) continuityKeepCount(history []messages.Message) int {
	if len(history) == 0 {
		return 0
	}

	policy := al.continuityRetentionPolicy()
	minKeep := policy.MinMessages
	if minKeep > len(history) {
		minKeep = len(history)
	}
	maxKeep := policy.MaxMessages
	if maxKeep > len(history) {
		maxKeep = len(history)
	}
	if maxKeep < minKeep {
		maxKeep = minKeep
	}

	contextWindow := al.contextWindow
	if contextWindow <= 0 && al.cfg != nil {
		contextWindow = al.cfg.Agents.Defaults.MaxTokens
	}
	if contextWindow <= 0 {
		return minKeep
	}

	targetTokens := int(float64(contextWindow) * policy.TargetContextRatio)
	if targetTokens <= 0 {
		return minKeep
	}

	keep := 0
	keptTokens := 0
	for i := len(history) - 1; i >= 0 && keep < maxKeep; i-- {
		msgTokens := observation.EstimateTokens(history[i].Content) + 4
		if keep >= minKeep && keptTokens+msgTokens > targetTokens {
			break
		}
		keptTokens += msgTokens
		keep++
	}
	if keep < minKeep {
		keep = minKeep
	}
	return keep
}

type oversizedRecoveryCandidate struct {
	Message       messages.Message
	OriginalIndex int
	TokenEstimate int
}

func (al *AgentLoop) persistOversizedRecoveryRefs(ctx context.Context, sessionKey string, omitted []oversizedRecoveryCandidate) ([]string, error) {
	if len(omitted) == 0 {
		return nil, nil
	}
	if al.memDelegate == nil {
		return nil, fmt.Errorf("memory delegate is not configured")
	}

	const maxPersistedRefs = 8
	refs := make([]string, 0, len(omitted))
	now := time.Now().UTC()

	for i, candidate := range omitted {
		if i >= maxPersistedRefs {
			break
		}
		nodeID := tools.DAGRecoveryNodePrefix + ids.New().String()
		record := tools.DAGRecoveryRecord{
			NodeID:        nodeID,
			SessionKey:    sessionKey,
			OriginalIndex: candidate.OriginalIndex,
			Role:          candidate.Message.Role,
			Content:       candidate.Message.Content,
			TokenEstimate: candidate.TokenEstimate,
			Reason:        "oversized_message_omitted_from_summary",
			CreatedAt:     now,
		}

		data, err := json.Marshal(record)
		if err != nil {
			return refs, fmt.Errorf("marshal DAG recovery record: %w", err)
		}
		if err := al.memDelegate.UpsertKV(ctx, pkg.NAME, tools.DAGRecoveryKVKey(sessionKey, nodeID), string(data)); err != nil {
			return refs, fmt.Errorf("persist DAG recovery record: %w", err)
		}
		refs = append(refs, nodeID)
	}
	return refs, nil
}

// summarizeSession summarizes the conversation history for a session.
// An optional escalationLevel controls summarization aggressiveness (1=normal, 2=aggressive, 3=deterministic).
func (al *AgentLoop) summarizeSession(parentCtx context.Context, sessionKey string, escalationLevel ...int) {
	level := 1
	if len(escalationLevel) > 0 && escalationLevel[0] > 0 {
		level = escalationLevel[0]
	}
	ctx, cancel := context.WithTimeout(parentCtx, 120*time.Second)
	defer cancel()

	history := al.sessions.GetHistory(sessionKey)
	summary := al.sessions.GetSummary(sessionKey)

	keepLast := al.continuityKeepCount(history)
	if len(history) <= keepLast {
		return
	}

	toSummarize := history[:len(history)-keepLast]

	// Oversized Message Guard: skip individual messages that would consume too
	// much of the summarizer's context. Use 40% of the window for the summarizer
	// input budget, reserving the rest for system prompt + summary output.
	// Oversized omissions are persisted as DAG recovery references.
	maxMessageTokens := al.contextWindow * 40 / 100
	if maxMessageTokens < 2048 {
		maxMessageTokens = 2048
	}
	validMessages := make([]messages.Message, 0)
	omitted := false
	omittedMessages := make([]oversizedRecoveryCandidate, 0)

	for idx, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgTokens := observation.EstimateTokens(m.Content)
		if msgTokens > maxMessageTokens {
			omitted = true
			omittedMessages = append(omittedMessages, oversizedRecoveryCandidate{
				Message:       m,
				OriginalIndex: idx,
				TokenEstimate: msgTokens,
			})
			continue
		}
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	// Multi-Part Summarization
	var finalSummary string
	if len(validMessages) > 10 {
		mid := len(validMessages) / 2
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatchEscalated(ctx, part1, "", level)
		s2, _ := al.summarizeBatchEscalated(ctx, part2, "", level)

		// Merge them
		mergePrompt := fmt.Sprintf("Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s", s1, s2)
		resp, err := al.callModel(ctx, mergePrompt)
		if err == nil {
			finalSummary = resp
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatchEscalated(ctx, validMessages, summary, level)
	}

	if omitted && finalSummary != "" {
		recoveryRefs, err := al.persistOversizedRecoveryRefs(ctx, sessionKey, omittedMessages)
		if err != nil {
			// Retry once with a fresh timeout
			retryCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
			recoveryRefs, err = al.persistOversizedRecoveryRefs(retryCtx, sessionKey, omittedMessages)
			retryCancel()
		}
		if err != nil {
			logger.ErrorCF("agent", "Failed to persist DAG recovery references after retry",
				map[string]interface{}{
					"session_key": sessionKey,
					"error":       err.Error(),
					"omitted":     len(omittedMessages),
				})
			finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
		} else if len(recoveryRefs) > 0 {
			finalSummary += fmt.Sprintf("\n[Note: %d oversized message(s) were omitted from this summary. Recovery refs: %s. Use dag_expand with node_id=<recovery-ref> to recover full content.]",
				len(omittedMessages), strings.Join(recoveryRefs, ", "))
		} else {
			finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
		}
	}

	// Quality gate: reject summaries that are too short to be useful.
	// A valid summary of even a short conversation should be 20+ chars.
	if len(strings.TrimSpace(finalSummary)) < 20 {
		finalSummary = ""
	}

	if finalSummary != "" {
		al.sessions.SetSummary(sessionKey, finalSummary)
		al.sessions.TruncateHistory(sessionKey, keepLast)
		al.sessions.Save(sessionKey)
		al.summarizeFailures.Delete(sessionKey)
	} else {
		var count int
		if v, ok := al.summarizeFailures.Load(sessionKey); ok {
			count = v.(int)
		}
		count++
		al.summarizeFailures.Store(sessionKey, count)

		const maxSummarizeFailures = 3
		emergencyKeep := al.continuityRetentionPolicy().FailureKeepMessages
		if count >= maxSummarizeFailures {
			logger.ErrorCF("agent", "Summarization failed repeatedly, force-truncating session",
				map[string]interface{}{
					"session":              sessionKey,
					"consecutive_failures": count,
					"keep":                 emergencyKeep,
				})
			al.sessions.TruncateHistory(sessionKey, emergencyKeep)
			al.sessions.Save(sessionKey)
			al.summarizeFailures.Delete(sessionKey)
		}
	}
}

// summarizeBatch summarizes a batch of messages using the Fantasy LanguageModel directly.
func (al *AgentLoop) summarizeBatch(ctx context.Context, batch []messages.Message, existingSummary string) (string, error) {
	var prompt strings.Builder
	prompt.WriteString("Provide a concise summary of this conversation segment, preserving core context and key points.\n")
	if existingSummary != "" {
		fmt.Fprintf(&prompt, "Existing context: %s\n", existingSummary)
	}
	prompt.WriteString("\nCONVERSATION:\n")
	for _, m := range batch {
		fmt.Fprintf(&prompt, "%s: %s\n", m.Role, m.Content)
	}

	return al.callModel(ctx, prompt.String())
}

// summarizeBatchEscalated applies escalation levels to summarization (LCM ADR-002).
// Level 1: normal summary (existing behavior)
// Level 2: aggressive bullet-point compression
// Level 3: deterministic truncation (no LLM call)
func (al *AgentLoop) summarizeBatchEscalated(ctx context.Context, batch []messages.Message, existingSummary string, level int) (string, error) {
	switch level {
	case 1:
		return al.summarizeBatch(ctx, batch, existingSummary)
	case 2:
		var prompt strings.Builder
		prompt.WriteString("Compress the following conversation into a VERY brief bullet-point list (max 5 bullets). Preserve only critical facts, decisions, and action items.\n")
		if existingSummary != "" {
			fmt.Fprintf(&prompt, "Prior context: %s\n", existingSummary)
		}
		prompt.WriteString("\nCONVERSATION:\n")
		for _, m := range batch {
			fmt.Fprintf(&prompt, "%s: %s\n", m.Role, m.Content)
		}
		return al.callModel(ctx, prompt.String())
	default:
		if len(batch) == 0 {
			return existingSummary, nil
		}
		first := batch[0]
		last := batch[len(batch)-1]
		return fmt.Sprintf("[Deterministic truncation of %d messages] First: %s: %s | Last: %s: %s",
			len(batch),
			first.Role, utils.Truncate(first.Content, 200),
			last.Role, utils.Truncate(last.Content, 200)), nil
	}
}

// callModel makes a direct call to the Fantasy LanguageModel (no tools, no agent loop).
// Used for summarization and other simple generation tasks.
func (al *AgentLoop) callModel(ctx context.Context, prompt string) (string, error) {
	temp := 0.3
	maxTokens := int64(1024)

	resp, err := al.languageModel.Generate(ctx, fantasy.Call{
		Prompt: fantasy.Prompt{
			fantasy.NewUserMessage(prompt),
		},
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
	})
	if err != nil {
		return "", err
	}
	return resp.Content.Text(), nil
}

// sessionsToMessagePairs converts the session history to observation.MessagePair
// for token estimation by the observation manager.
func (al *AgentLoop) sessionsToMessagePairs(sessionKey string) []observation.MessagePair {
	history := al.sessions.GetHistory(sessionKey)
	pairs := make([]observation.MessagePair, len(history))
	for i, m := range history {
		pairs[i] = observation.MessagePair{Role: m.Role, Content: m.Content}
	}
	return pairs
}

// contextTreeCacheEntry caches rendered query-selected history blocks per session.
type contextTreeCacheEntry struct {
	msgCount int
	query    string
	rendered string
}

// applyContextTreeSelection selects relevant historical context via query-adaptive
// Context Tree scoring and keeps only the raw tail as messages.
func (al *AgentLoop) applyContextTreeSelection(ctx context.Context, sessionKey, query string, history []messages.Message) []messages.Message {
	const minHistoryForSelection = 16

	if len(history) < minHistoryForSelection {
		al.contextBuilder.SetContextTreeBlock("")
		return history
	}

	budget := dag.ComputeBudget(al.contextWindow, dag.DefaultBudgetConfig())
	tailCount := dag.TailMessageCount(budget.RawTail)
	if tailCount >= len(history) {
		al.contextBuilder.SetContextTreeBlock("")
		return history
	}

	compressible := history[:len(history)-tailCount]
	tail := history[len(history)-tailCount:]

	for len(tail) > 0 && tail[0].Role == "tool" && len(compressible) > 0 {
		tail = append([]messages.Message{compressible[len(compressible)-1]}, tail...)
		compressible = compressible[:len(compressible)-1]
	}

	if len(compressible) == 0 {
		al.contextBuilder.SetContextTreeBlock("")
		return history
	}

	cacheHit := false
	if cached, ok := al.contextTreeCache.Load(sessionKey); ok {
		entry := cached.(contextTreeCacheEntry)
		if entry.msgCount == len(compressible) && entry.query == query {
			al.contextBuilder.SetContextTreeBlock(entry.rendered)
			return tail
		}
	}

	tree := contexttree.NewContextTree(contexttree.DefaultScoringConfig())
	rootID := tree.Root.ID
	for _, m := range compressible {
		tree.AddNode(rootID, contextNodeTypeForRole(m.Role), m.Content, nil, contexttree.ExtractTerms(m.Content))
	}

	queryTerms := contexttree.ExtractTerms(query)
	if len(queryTerms) == 0 && len(tail) > 0 {
		queryTerms = contexttree.ExtractTerms(tail[len(tail)-1].Content)
	}

	scores := tree.ScoreAll(nil, queryTerms)
	nodes := make([]*contexttree.ContextNode, 0, len(tree.NodeIndex)-1)
	for id, node := range tree.NodeIndex {
		if node.Type == contexttree.NodeTypeRoot {
			continue
		}
		node.TotalScore = scores[id]
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].TotalScore == nodes[j].TotalScore {
			return nodes[i].CreatedAt.After(nodes[j].CreatedAt)
		}
		return nodes[i].TotalScore > nodes[j].TotalScore
	})

	selectionBudget := budget.DAGSummaries
	if selectionBudget <= 0 {
		selectionBudget = 512
	}
	selected := make([]*contexttree.ContextNode, 0, len(nodes))
	usedTokens := 0
	for _, node := range nodes {
		nodeTokens := observation.EstimateTokens(node.Content)
		if nodeTokens == 0 {
			continue
		}
		if usedTokens+nodeTokens > selectionBudget {
			continue
		}
		selected = append(selected, node)
		usedTokens += nodeTokens
	}

	rendered := renderContextTreeSelection(selected)
	al.contextBuilder.SetContextTreeBlock(rendered)
	al.contextTreeCache.Store(sessionKey, contextTreeCacheEntry{msgCount: len(compressible), query: query, rendered: rendered})

	logger.DebugCF("agent", "Context-Tree selection applied", map[string]interface{}{
		"total_msgs":      len(history),
		"compressed_msgs": len(compressible),
		"tail_msgs":       len(tail),
		"selected_nodes":  len(selected),
		"selected_tokens": usedTokens,
		"cache_hit":       cacheHit,
	})

	return tail
}

func contextNodeTypeForRole(role string) contexttree.NodeType {
	switch role {
	case "tool":
		return contexttree.NodeTypeToolCall
	case "assistant":
		return contexttree.NodeTypeSummary
	default:
		return contexttree.NodeTypeMessage
	}
}

func renderContextTreeSelection(selected []*contexttree.ContextNode) string {
	if len(selected) == 0 {
		return ""
	}
	var b strings.Builder
	for _, node := range selected {
		line := strings.ReplaceAll(node.Content, "\n", " ")
		line = strings.TrimSpace(line)
		if len(line) > 320 {
			line = line[:317] + "..."
		}
		fmt.Fprintf(&b, "[%s score=%.3f] %s\n", node.Type, node.TotalScore, line)
	}
	return strings.TrimSpace(b.String())
}

func (al *AgentLoop) estimateTokens(msgs []messages.Message) int {
	pairs := make([]observation.MessagePair, 0, len(msgs))
	for _, m := range msgs {
		pairs = append(pairs, observation.MessagePair{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return observation.EstimateMessagesTokens(pairs)
}
