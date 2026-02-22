package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// maybeSummarize only triggers emergency compression when hard limits are exceeded.
// Normal background compaction is intentionally disabled for the unified kernel.
func (al *AgentLoop) maybeSummarize(ctx context.Context, sessionKey, channel, chatID string) {
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	criticalThreshold := al.contextWindow * 95 / 100

	if tokenEstimate > criticalThreshold {
		al.forceCompression(ctx, sessionKey, channel, chatID)
	}
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

		al.summarizeSession(ctx, sessionKey)
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
func (al *AgentLoop) summarizeSession(parentCtx context.Context, sessionKey string) {
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

		s1, _ := al.summarizeBatch(ctx, part1, "")
		s2, _ := al.summarizeBatch(ctx, part2, "")

		// Merge them
		mergePrompt := fmt.Sprintf("Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s", s1, s2)
		resp, err := al.callModel(ctx, mergePrompt)
		if err == nil {
			finalSummary = resp
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, validMessages, summary)
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

// dagCacheEntry holds the cached DAG compression output for a session,
// enabling skip of recompression and repersistence when the compressible
// portion hasn't grown since the last call.
type dagCacheEntry struct {
	msgCount      int
	rendered      string
	dag           *dag.DAG
	persistFailed bool
}

// applyDAGCompression compresses old history into a DAG summary block and
// returns only the tail messages that should be passed as raw conversation.
// The compressed portion is injected into the system prompt via contextBuilder.
// When memDelegate implements dag.DAGPersister, the DAG is persisted for dag_expand/describe/grep.
//
// Incremental optimization: if the compressible message count matches the
// cached count, the previous DAG and rendered block are reused without
// recompression or repersistence.
func (al *AgentLoop) applyDAGCompression(ctx context.Context, sessionKey string, history []messages.Message) []messages.Message {
	const minHistoryForDAG = 16

	if len(history) < minHistoryForDAG {
		al.contextBuilder.SetDAGBlock("")
		return history
	}

	budget := dag.ComputeBudget(al.contextWindow, dag.DefaultBudgetConfig())
	tailCount := dag.TailMessageCount(budget.RawTail)
	if tailCount >= len(history) {
		al.contextBuilder.SetDAGBlock("")
		return history
	}

	// Split: compress old, keep tail raw
	compressible := history[:len(history)-tailCount]
	tail := history[len(history)-tailCount:]

	// Tool-call-aware: don't split on a "tool" message
	for len(tail) > 0 && tail[0].Role == "tool" && len(compressible) > 0 {
		tail = append([]messages.Message{compressible[len(compressible)-1]}, tail...)
		compressible = compressible[:len(compressible)-1]
	}

	if len(compressible) == 0 {
		al.contextBuilder.SetDAGBlock("")
		return history
	}

	// Check DAG cache: skip recompression if compressible count hasn't changed
	if cached, ok := al.dagCache.Load(sessionKey); ok {
		entry := cached.(dagCacheEntry)
		if entry.msgCount == len(compressible) {
			al.contextBuilder.SetDAGBlock(entry.rendered)
			// Retry failed persistence from previous turn
			if entry.persistFailed {
				al.retryDAGPersist(ctx, sessionKey, entry)
			}
			return tail
		}
	}

	dagMsgs := make([]dag.Message, len(compressible))
	for i, m := range compressible {
		dagMsgs[i] = dag.Message{Role: m.Role, Content: m.Content}
	}

	compressor := dag.NewCompressor(dag.DefaultCompressorConfig())
	d := compressor.Compress(dagMsgs)

	rendered := dag.RenderDAGForBudget(d, budget.DAGSummaries)
	al.contextBuilder.SetDAGBlock(rendered)

	// Cache the result
	al.dagCache.Store(sessionKey, dagCacheEntry{
		msgCount: len(compressible),
		rendered: rendered,
		dag:      d,
	})

	// Persist DAG for dag_expand, dag_describe, dag_grep (additive; in-memory behavior unchanged)
	persistOK := al.tryDAGPersist(ctx, sessionKey, d, len(compressible))
	if !persistOK {
		// Mark for retry on next cache hit
		al.dagCache.Store(sessionKey, dagCacheEntry{
			msgCount:      len(compressible),
			rendered:      rendered,
			dag:           d,
			persistFailed: true,
		})
	}

	logger.DebugCF("agent", "DAG compression applied",
		map[string]interface{}{
			"total_msgs":      len(history),
			"compressed_msgs": len(compressible),
			"tail_msgs":       len(tail),
			"dag_nodes":       len(d.Nodes),
			"cache_hit":       false,
		})

	return tail
}

func (al *AgentLoop) tryDAGPersist(ctx context.Context, sessionKey string, d *dag.DAG, msgCount int) bool {
	dp, ok := al.memDelegate.(dag.DAGPersister)
	if !ok {
		return true
	}
	if err := dp.PersistDAG(ctx, pkg.NAME, sessionKey, &dag.PersistSnapshot{
		FromMsgIdx: 0,
		ToMsgIdx:   msgCount,
		MsgCount:   msgCount,
		DAG:        d,
	}); err != nil {
		logger.WarnCF("agent", "DAG persist failed (will retry next turn)",
			map[string]interface{}{"error": err.Error(), "session_key": sessionKey})
		return false
	}
	return true
}

func (al *AgentLoop) retryDAGPersist(ctx context.Context, sessionKey string, entry dagCacheEntry) {
	if al.tryDAGPersist(ctx, sessionKey, entry.dag, entry.msgCount) {
		al.dagCache.Store(sessionKey, dagCacheEntry{
			msgCount:      entry.msgCount,
			rendered:      entry.rendered,
			dag:           entry.dag,
			persistFailed: false,
		})
	}
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
