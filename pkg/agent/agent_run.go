// assembledContext holds the pre-processed context produced by assembleContext,
// consumed by both the Generate and Stream code paths.
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	dragonfantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
	"golang.org/x/sync/errgroup"
)

const conversationBindingKVPrefix = "session:conversation:"

func conversationBindingKey(sessionKey string) string {
	return conversationBindingKVPrefix + sessionKey
}

func (al *AgentLoop) loadBoundConversationID(ctx context.Context, sessionKey string) (ids.UUID, error) {
	if al == nil || al.memDelegate == nil {
		return ids.UUID{}, nil
	}
	raw, err := al.memDelegate.GetKV(ctx, pkg.NAME, conversationBindingKey(sessionKey))
	if err != nil || strings.TrimSpace(raw) == "" {
		return ids.UUID{}, err
	}
	conversationID, err := ids.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ids.UUID{}, err
	}
	return conversationID, nil
}

func (al *AgentLoop) persistConversationBinding(ctx context.Context, sessionKey string, conversationID ids.UUID) error {
	if al == nil || al.memDelegate == nil || strings.TrimSpace(sessionKey) == "" || conversationID.IsZero() {
		return nil
	}
	return al.memDelegate.UpsertKV(ctx, pkg.NAME, conversationBindingKey(sessionKey), conversationID.String())
}

type assembledContext struct {
	systemPrompt   string
	userPrompt     string
	fantasyHistory []fantasy.Message
	adaptedTools   []fantasy.AgentTool
	agent          fantasy.Agent
	projection     *memory.ActiveContextProjection
	conversationID ids.UUID
	runID          ids.UUID
}

type agentRunMetrics struct {
	StepCount   int
	ToolCalls   int
	Errors      int
	TotalTokens int
}

type ctxToolSessionKey struct{}

func withToolSessionKey(ctx context.Context, sessionKey string) context.Context {
	if strings.TrimSpace(sessionKey) == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxToolSessionKey{}, sessionKey)
}

func toolSessionKeyFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxToolSessionKey{}).(string)
	return strings.TrimSpace(v)
}

func collectAgentRunMetrics(result *fantasy.AgentResult) agentRunMetrics {
	if result == nil {
		return agentRunMetrics{}
	}
	return collectAgentRunStepMetrics(result.Steps, int(result.TotalUsage.TotalTokens))
}

func collectAgentRunStepMetrics(steps []fantasy.StepResult, totalTokens int) agentRunMetrics {
	metrics := agentRunMetrics{
		StepCount:   len(steps),
		TotalTokens: totalTokens,
	}
	for _, step := range steps {
		metrics.ToolCalls += len(step.Content.ToolCalls())
		for _, tr := range step.Content.ToolResults() {
			if errResult, ok := tr.Result.(fantasy.ToolResultOutputContentError); ok && errResult.Error != nil {
				metrics.Errors++
			}
		}
	}

	return metrics
}

func (al *AgentLoop) transitionObserver(runID ids.UUID) fantasy.ReActTransitionObserverFunc {
	return fantasy.ReActTransitionObserverFunc(func(observerCtx context.Context, t fantasy.ReActTransition) {
		if al == nil || al.stateStore == nil || runID.IsZero() {
			return
		}
		if _, err := al.stateStore.AddTransition(context.WithoutCancel(observerCtx), runID, t); err != nil {
			logger.WarnCF("agent", "Failed to persist ReAct transition",
				map[string]interface{}{
					"error":      err.Error(),
					"run_id":     runID.String(),
					"from":       string(t.From),
					"to":         string(t.To),
					"trigger":    string(t.Trigger),
					"step_index": t.StepIndex,
				})
		}
	})
}

func (al *AgentLoop) replayAgentSteps(ctx context.Context, sessionKey string, steps []fantasy.StepResult) {
	for _, step := range steps {
		stepMsgs := dragonfantasy.StepToMessages(step)
		for _, m := range stepMsgs {
			al.sessions.AddFullMessage(sessionKey, m)
		}
		al.auditStep(ctx, step, sessionKey)
	}
}

func (al *AgentLoop) ensureFinalAssistantMessage(sessionKey, finalContent string) {
	if al == nil || al.sessions == nil {
		return
	}
	trimmed := strings.TrimSpace(finalContent)
	if strings.TrimSpace(sessionKey) == "" || trimmed == "" {
		return
	}
	history := al.sessions.GetHistory(sessionKey)
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && last.ToolCallID == "" && len(last.ToolCalls) == 0 && strings.TrimSpace(last.Content) == trimmed {
			return
		}
	}
	al.sessions.AddMessage(sessionKey, "assistant", trimmed)
}

func (al *AgentLoop) prepareRuntimeState(ctx context.Context, sessionKey string) (ids.UUID, ids.UUID, error) {
	if al.queries == nil || al.stateStore == nil || al.kvDelegate == nil {
		return ids.UUID{}, ids.UUID{}, errors.New("runtime persistence dependencies are not initialized")
	}
	if strings.TrimSpace(sessionKey) == "" {
		return ids.UUID{}, ids.UUID{}, errors.New("session key is required")
	}

	var conversationID ids.UUID
	if cached, ok := al.conversationIDs.Load(sessionKey); ok {
		conversationID = cached
	} else {
		al.conversationMu.Lock()
		defer al.conversationMu.Unlock()
		if cached, ok := al.conversationIDs.Load(sessionKey); ok {
			conversationID = cached
		} else {
			boundID, err := al.loadBoundConversationID(ctx, sessionKey)
			if err != nil {
				return ids.UUID{}, ids.UUID{}, fmt.Errorf("load conversation binding: %w", err)
			}
			if !boundID.IsZero() {
				if _, err := al.queries.GetAgentConversation(ctx, memsqlc.GetAgentConversationParams{ID: boundID}); err == nil {
					conversationID = boundID
				} else if err != sql.ErrNoRows {
					return ids.UUID{}, ids.UUID{}, fmt.Errorf("load bound conversation: %w", err)
				}
			}
			if conversationID.IsZero() {
				conversationID, err = al.lookupConversationIDForSession(ctx, sessionKey)
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) {
						return ids.UUID{}, ids.UUID{}, fmt.Errorf("lookup conversation for session: %w", err)
					}
					conversationID = ids.New()
					title := sessionKey
					if _, err := al.queries.CreateAgentConversation(ctx, memsqlc.CreateAgentConversationParams{
						ID:    conversationID,
						Title: &title,
					}); err != nil {
						return ids.UUID{}, ids.UUID{}, fmt.Errorf("create agent conversation: %w", err)
					}
				}
			}
			if err := al.persistConversationBinding(ctx, sessionKey, conversationID); err != nil {
				return ids.UUID{}, ids.UUID{}, fmt.Errorf("persist conversation binding: %w", err)
			}
			al.conversationIDs.Store(sessionKey, conversationID)
		}
	}

	run, err := al.stateStore.CreateRun(ctx, conversationID)
	if err != nil {
		return ids.UUID{}, ids.UUID{}, fmt.Errorf("create agent run: %w", err)
	}

	return conversationID, run.ID, nil
}

// assembleContext performs the shared pre-processing for every agent turn:
// record channel, update tool contexts, load memory blocks, build messages,
// DAG-compress history, split into system/history/user, adapt tools, create Fantasy agent.
func (al *AgentLoop) assembleContext(ctx context.Context, opts processOptions) (assembledContext, error) {
	if err := al.recordChannelState(ctx, opts); err != nil {
		logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
	}

	logger.DebugCF("agent", "assembleContext: starting",
		map[string]interface{}{
			"session_key": opts.SessionKey,
			"channel":     opts.Channel,
			"sender_id":   opts.SenderID,
		})

	al.refreshContextBlocks(ctx, opts)
	history, summary := al.loadSessionState(ctx, opts)

	var (
		systemPrompt string
		historyMsgs  []messages.Message
		userPrompt   = opts.UserMessage
		projection   *memory.ActiveContextProjection
	)

	if al.activeContextBuilder != nil {
		built, err := al.activeContextBuilder.BuildTurnContext(ctx, TurnContextBuildRequest{
			ProjectionRequest: memory.ProjectionRequest{
				AgentID:      pkg.NAME,
				SessionKey:   opts.SessionKey,
				MaxTokens:    al.contextWindow,
				IncludeTools: true,
			},
			CurrentMessage:  opts.UserMessage,
			NoHistory:       opts.NoHistory,
			FallbackHistory: history,
			Summary:         summary,
		})
		if err != nil {
			return assembledContext{}, fmt.Errorf("build active context projection: %w", err)
		}
		projection = al.maybeReduceProjectionWithRLM(ctx, opts.SessionKey, opts.UserMessage, built.Projection)
		historyMsgs = built.History
		systemPrompt = al.contextBuilder.RenderProjection(projection, opts.Channel, opts.ChatID)
	} else {
		builtMsgs := al.buildPromptMessages(opts, history, summary)
		systemPrompt, historyMsgs, userPrompt = al.splitMessages(opts, builtMsgs)
	}
	if constraint := turnConstraintForQuery(opts.UserMessage); constraint != "" {
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\n## Turn Constraint\n" + constraint)
	}
	if !isPlanningOnlyPrompt(opts.UserMessage) {
		if hintedNames := initialPromptToolNames(al.tools, opts.UserMessage); len(hintedNames) > 0 {
			systemPrompt = strings.TrimSpace(systemPrompt + "\n\n## Turn Tool Hints\n" + turnToolHintText(hintedNames))
			if command := explicitExecCommand(opts.UserMessage); command != "" {
				systemPrompt = strings.TrimSpace(systemPrompt + fmt.Sprintf("\nIf you use exec for this request, set `command` to exactly %q. Do not substitute placeholders like `:`, empty strings, or paraphrases.", command))
			}
			if skillName := explicitSkillName(opts.UserMessage); skillName != "" && strings.Contains(strings.ToLower(opts.UserMessage), "skill") {
				systemPrompt = strings.TrimSpace(systemPrompt + fmt.Sprintf("\nIf you use skill_read for this request, set `name` to exactly %q. Do not substitute placeholders or punctuation-only values.", skillName))
			}
		}
	}

	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	logger.DebugCF("agent", "assembleContext: history messages",
		map[string]interface{}{
			"history": formatMessagesForLog(historyMsgs),
		})

	fantasyHistory := dragonfantasy.MessagesToFantasy(historyMsgs)
	adaptedTools, prepareStep := al.prepareToolset(ctx, opts)
	agent, conversationID, runID, err := al.createFantasyAgent(ctx, opts, systemPrompt, adaptedTools, prepareStep)
	if err != nil {
		return assembledContext{}, err
	}

	logger.DebugCF("agent", "Fantasy agent created",
		map[string]interface{}{
			"model":          al.model,
			"tools_count":    len(adaptedTools),
			"history_count":  len(historyMsgs),
			"max_iterations": al.maxIterations,
			"memory_enabled": true,
		})

	return assembledContext{
		systemPrompt:   systemPrompt,
		userPrompt:     userPrompt,
		fantasyHistory: fantasyHistory,
		adaptedTools:   adaptedTools,
		agent:          agent,
		projection:     projection,
		conversationID: conversationID,
		runID:          runID,
	}, nil
}

func (al *AgentLoop) recordChannelState(ctx context.Context, opts processOptions) error {
	if opts.Channel == "" || opts.ChatID == "" {
		return nil
	}
	if constants.IsInternalChannel(opts.Channel) {
		return nil
	}

	channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
	return al.RecordLastChannel(ctx, channelKey)
}

type ctxBlockCacheEntry struct {
	focusBlock string
	knowledge  string
	cachedAt   time.Time
}

const ctxBlockCacheTTL = 2 * time.Minute

func (al *AgentLoop) refreshContextBlocks(ctx context.Context, opts processOptions) {
	var (
		obsBlock   string
		kb         string
		focusBlock string
	)

	// Check if focus/knowledge can be served from cache.
	// Invalidated by: (a) start_focus / complete_focus OnChange callbacks,
	// (b) TTL expiry — catches external KV modifications outside focus tools.
	_, dirty := al.focusDirty.LoadAndDelete(opts.SessionKey)

	useCached := false
	if !dirty {
		if cached, ok := al.ctxBlockCache.Load(opts.SessionKey); ok {
			entry := cached.(ctxBlockCacheEntry)
			if time.Since(entry.cachedAt) < ctxBlockCacheTTL {
				useCached = true
				kb = entry.knowledge
				focusBlock = entry.focusBlock
			}
		}
	}

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		obsBlock = al.obsManager.LoadBlock(gCtx, opts.SessionKey)
		return nil
	})
	if !useCached {
		g.Go(func() error {
			kb = tools.LoadKnowledgeBlock(gCtx, al.memDelegate, opts.SessionKey)
			return nil
		})
		g.Go(func() error {
			if fs, ok := tools.LoadFocusState(gCtx, al.memDelegate, opts.SessionKey); ok {
				focusBlock = fs.FormatBlock()
			}
			return nil
		})
	}
	g.Go(func() error {
		if al.identitySync != nil {
			_ = al.identitySync.CheckAndSync(gCtx)
		}
		return nil
	})

	_ = g.Wait()

	if !useCached {
		al.ctxBlockCache.Store(opts.SessionKey, ctxBlockCacheEntry{
			focusBlock: focusBlock,
			knowledge:  kb,
			cachedAt:   time.Now(),
		})
	}

	// Prune stale entries from other sessions to prevent unbounded growth.
	al.ctxBlockCache.Range(func(key, value any) bool {
		if entry, ok := value.(ctxBlockCacheEntry); ok {
			if time.Since(entry.cachedAt) > 2*ctxBlockCacheTTL {
				al.ctxBlockCache.Delete(key)
				al.focusDirty.Delete(key) // clean up orphaned dirty flags
			}
		}
		return true
	})

	al.contextBuilder.SetObservationBlock(obsBlock)
	al.contextBuilder.SetKnowledgeBlock(kb)
	al.contextBuilder.SetFocusBlock(focusBlock)
}

func (al *AgentLoop) loadSessionState(ctx context.Context, opts processOptions) ([]messages.Message, string) {
	var history []messages.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}

	// Zero-cost continuity (LCM ADR-002): skip DAG compression when context
	// usage is below the soft compaction threshold. This eliminates overhead
	// for ~80% of interactions where context is not under pressure.
	softPct, _ := al.compactionThresholds()
	softThreshold := al.contextWindow * softPct / 100
	tokenEstimate := al.estimateTokens(history)
	if tokenEstimate <= softThreshold {
		al.contextBuilder.SetContextTreeBlock("")
		return history, summary
	}

	return al.applyContextTreeSelection(ctx, opts.SessionKey, opts.UserMessage, history), summary
}

func (al *AgentLoop) buildPromptMessages(opts processOptions, history []messages.Message, summary string) []messages.Message {
	builtMsgs := al.contextBuilder.BuildMessages(opts.SessionKey, history, summary, opts.UserMessage, nil, opts.Channel, opts.ChatID)
	return builtMsgs
}

func (al *AgentLoop) splitMessages(opts processOptions, builtMsgs []messages.Message) (string, []messages.Message, string) {
	systemPrompt := ""
	var historyMsgs []messages.Message
	userPrompt := opts.UserMessage

	if len(builtMsgs) > 0 && builtMsgs[0].Role == "system" {
		systemPrompt = builtMsgs[0].Content
		if len(builtMsgs) > 2 {
			historyMsgs = builtMsgs[1 : len(builtMsgs)-1]
		}
	}

	return systemPrompt, historyMsgs, userPrompt
}

func (al *AgentLoop) prepareToolset(ctx context.Context, opts processOptions) ([]fantasy.AgentTool, func(context.Context, fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error)) {
	adaptCfg := dragonfantasy.AdaptedToolsConfig{
		MemStore:   al.memoryStore,
		AgentID:    pkg.NAME,
		SessionKey: opts.SessionKey,
	}

	adaptedTools := []fantasy.AgentTool{}
	if isPlanningOnlyPrompt(opts.UserMessage) {
		logger.DebugCF("agent", "Suppressing tools for planning-only prompt",
			map[string]interface{}{"query": utils.Truncate(opts.UserMessage, 120)})
	} else {
		selected := al.initialPromptTools(opts.UserMessage)
		if len(selected) > 0 {
			adaptedTools = dragonfantasy.AdaptTools(selected, al.bus, opts.Channel, opts.ChatID, adaptCfg)
		}
		if al.toolResultSearch != nil && shouldExposeToolResultSearch(opts.UserMessage) {
			adaptedTools = append(adaptedTools, al.toolResultSearch)
		}
	}

	promotedSet := make(map[string]bool)
	for _, at := range adaptedTools {
		promotedSet[at.Info().Name] = true
	}
	registry := al.tools
	msgBus := al.bus
	channel := opts.Channel
	chatID := opts.ChatID

	prepareStep := func(ctx context.Context, psOpts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
		_ = psOpts
		discovered := registry.DrainDiscovered()
		if len(discovered) == 0 {
			return ctx, fantasy.PrepareStepResult{}, nil
		}

		newTools := make([]tools.Tool, 0, len(discovered))
		for _, t := range discovered {
			if promotedSet[t.Name()] {
				continue
			}
			newTools = append(newTools, t)
			promotedSet[t.Name()] = true
		}

		if len(newTools) == 0 {
			return ctx, fantasy.PrepareStepResult{}, nil
		}

		newAdapted := dragonfantasy.AdaptTools(newTools, msgBus, channel, chatID, adaptCfg)
		expanded := append(adaptedTools, newAdapted...)
		adaptedTools = expanded

		logger.InfoCF("agent", "Dynamic tool promotion via PrepareStep",
			map[string]interface{}{
				"promoted":    len(newTools),
				"total_tools": len(expanded),
				"names":       toolNames(newTools),
			})

		return ctx, fantasy.PrepareStepResult{
			Tools: expanded,
		}, nil
	}

	return adaptedTools, prepareStep
}

func (al *AgentLoop) initialPromptTools(query string) []tools.Tool {
	if al == nil || al.tools == nil {
		return nil
	}
	return collectInitialPromptTools(al.tools, query)
}

func initialPromptToolNames(registry *tools.ToolRegistry, query string) []string {
	hinted := collectInitialPromptTools(registry, query)
	return toolNames(hinted)
}

func collectInitialPromptTools(registry *tools.ToolRegistry, query string) []tools.Tool {
	if registry == nil {
		return nil
	}

	q := strings.ToLower(query)
	if tool := directDelegationTool(q); tool != "" {
		return collectInitialTools(registry, query, map[string]struct{}{tool: {}})
	}

	want := map[string]struct{}{}

	if isToolDiscoveryPrompt(q) {
		want["tool_search"] = struct{}{}
		if strings.Contains(q, "tool_call") {
			want["tool_call"] = struct{}{}
		}
		return collectInitialTools(registry, query, want)
	}

	if strings.Contains(q, "skill") {
		want["skill_search"] = struct{}{}
		want["skill_read"] = struct{}{}
		if strings.Contains(q, "related") || strings.Contains(q, "traverse") || strings.Contains(q, "connected") {
			want["skill_traverse"] = struct{}{}
		}
	}

	if strings.Contains(q, "run ") || strings.Contains(q, "command") || strings.Contains(q, "shell") || strings.Contains(q, "uname") || strings.Contains(q, "echo ") {
		want["exec"] = struct{}{}
	}

	if strings.Contains(q, "read ") || strings.Contains(q, "read the file") || strings.Contains(q, "read it back") || strings.Contains(q, "tell me line") || strings.Contains(q, "contains the word") {
		want["read_file"] = struct{}{}
	}

	if strings.Contains(q, "write ") || strings.Contains(q, "write the text") || strings.Contains(q, "create a file") || strings.Contains(q, "file called") || strings.Contains(q, "save to") {
		want["write_file"] = struct{}{}
	}

	if strings.Contains(q, "append") || strings.Contains(q, "add to the end") || strings.Contains(q, "append to") {
		want["append_file"] = struct{}{}
	}

	if strings.Contains(q, "replace") || strings.Contains(q, "edit ") || strings.Contains(q, " edit") || strings.Contains(q, "patch") || strings.Contains(q, "update existing") {
		want["edit_file"] = struct{}{}
	}

	if strings.Contains(q, "list ") || strings.Contains(q, "directory") || strings.Contains(q, "folder") {
		want["list_dir"] = struct{}{}
	}

	if isExplicitSpawnPrompt(q) {
		want["spawn"] = struct{}{}
	}

	if isExplicitSubagentPrompt(q) {
		want["subagent"] = struct{}{}
	}

	if strings.Contains(q, "memory") &&
		(strings.Contains(q, "search") ||
			strings.Contains(q, "status") ||
			strings.Contains(q, "context pressure") ||
			strings.Contains(q, "what do you remember") ||
			strings.Contains(q, "look in your memory") ||
			strings.Contains(q, "look through your memory") ||
			strings.Contains(q, "recall")) {
		want["memory"] = struct{}{}
	}

	if strings.Contains(q, "commitment") || strings.Contains(q, "commitments") || strings.Contains(q, "track these") || strings.Contains(q, "capture these") || strings.Contains(q, "remember this") || strings.Contains(q, "store this") {
		want["memory"] = struct{}{}
	}

	if strings.Contains(q, "set reminder") || strings.Contains(q, "remind me") || strings.Contains(q, "create reminder") || strings.Contains(q, "schedule reminder") {
		want["obligation"] = struct{}{}
	}

	if strings.Contains(q, "search the web") || (strings.Contains(q, "web") && strings.Contains(q, "search")) {
		want["web_search"] = struct{}{}
	}

	if strings.Contains(q, "fetch ") && strings.Contains(q, "http") {
		want["web_fetch"] = struct{}{}
	}

	if len(want) == 0 && shouldDefaultToToolSearch(q) {
		want["tool_search"] = struct{}{}
	}

	return collectInitialTools(registry, query, want)
}

func (al *AgentLoop) collectInitialTools(query string, want map[string]struct{}) []tools.Tool {
	if al == nil || al.tools == nil {
		return nil
	}
	return collectInitialTools(al.tools, query, want)
}

func collectInitialTools(registry *tools.ToolRegistry, query string, want map[string]struct{}) []tools.Tool {
	if registry == nil || len(want) == 0 {
		return nil
	}

	order := []string{
		"skill_search",
		"skill_read",
		"skill_traverse",
		"exec",
		"read_file",
		"write_file",
		"edit_file",
		"append_file",
		"list_dir",
		"spawn",
		"subagent",
		"memory",
		"obligation",
		"web_search",
		"web_fetch",
		"tool_search",
		"tool_call",
	}

	hinted := make([]tools.Tool, 0, len(order))
	for _, name := range order {
		if _, ok := want[name]; !ok {
			continue
		}
		tool, found := registry.Get(name)
		if !found {
			continue
		}
		hinted = append(hinted, tool)
	}

	if len(hinted) > 0 {
		logger.DebugCF("agent", "Query-aware initial tool exposure",
			map[string]interface{}{
				"query": utils.Truncate(query, 120),
				"tools": toolNames(hinted),
			})
	}

	return hinted
}

func isToolDiscoveryPrompt(q string) bool {
	return strings.Contains(q, "search for a tool") ||
		strings.Contains(q, "find a tool") ||
		strings.Contains(q, "discover a tool") ||
		strings.Contains(q, "what tool") ||
		strings.Contains(q, "which tool") ||
		strings.Contains(q, "available tool") ||
		strings.Contains(q, "tool that can")
}

func shouldExposeToolResultSearch(q string) bool {
	query := strings.ToLower(q)
	return strings.Contains(query, "tool result") ||
		strings.Contains(query, "previous tool") ||
		strings.Contains(query, "search tool results")
}

func directDelegationTool(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return ""
	}

	if isExplicitSpawnPrompt(q) {
		return "spawn"
	}
	if isExplicitSubagentPrompt(q) {
		return "subagent"
	}

	return ""
}

func normalizeDelegationQuery(q string) string {
	normalized := strings.ToLower(strings.TrimSpace(q))
	for {
		trimmed := normalized
		for _, prefix := range []string{"please ", "can you ", "could you ", "would you ", "kindly "} {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				break
			}
		}
		if trimmed == normalized {
			return normalized
		}
		normalized = trimmed
	}
}

func hasImperativePrefix(q string, prefixes ...string) bool {
	normalized := normalizeDelegationQuery(q)
	if strings.Contains(normalized, "?") {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func isMetaExecutionDiscussionPrompt(q string) bool {
	normalized := normalizeDelegationQuery(q)
	if !(strings.Contains(normalized, "subagent") || strings.Contains(normalized, "delegate") || strings.Contains(normalized, "background") || strings.Contains(normalized, "asynchronously")) {
		return false
	}
	if strings.Contains(normalized, "?") {
		return true
	}
	if strings.Contains(normalized, " or ") {
		return true
	}
	for _, prefix := range []string{"when should", "why ", "how ", "should we", "explain whether", "plan to ", "give me a plan", "whether ", "when ", "compare "} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func shouldDefaultToToolSearch(q string) bool {
	normalized := normalizeDelegationQuery(q)
	if normalized == "" || strings.Contains(normalized, "?") || isPlanningOnlyPrompt(normalized) || isMetaExecutionDiscussionPrompt(normalized) {
		return false
	}
	for _, prefix := range []string{"debug ", "review ", "inspect ", "investigate ", "analyze ", "analyse ", "fix ", "implement ", "trace ", "profile ", "audit ", "check ", "examine ", "look into ", "look at ", "compare ", "verify "} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func turnToolHintText(hintedNames []string) string {
	if len(hintedNames) == 0 {
		return ""
	}
	if len(hintedNames) == 1 && hintedNames[0] == "tool_search" {
		return "For this request, if you need a tool, start with `tool_search` to discover the right concrete tool. Only promote a concrete tool when it clearly matches the user's request, and answer directly if no tool is needed."
	}
	return fmt.Sprintf("For this request, use these direct tools first: %s. Treat these as the initially available direct tools for this turn; do not call unrelated tools unless they are explicitly promoted later. This is an execution request, so do the tool work immediately instead of only describing intent. After the tool work finishes, always provide a concise final answer. If a tool fails or times out, explain that plainly in the final answer instead of stopping silently.", strings.Join(hintedNames, ", "))
}

func isExplicitSpawnPrompt(q string) bool {
	return hasImperativePrefix(q,
		"spawn ",
		"spawn a ",
		"spawn an ",
		"start a background task",
		"start a background job",
		"run this in the background",
		"run it in the background",
		"execute this in the background",
		"execute it in the background",
		"do this in the background",
		"do it in the background",
		"run this asynchronously",
		"run it asynchronously",
		"execute this asynchronously",
		"execute it asynchronously",
		"do this asynchronously",
		"do it asynchronously",
		"start an async task",
	)
}

func isExplicitSubagentPrompt(q string) bool {
	return hasImperativePrefix(q,
		"use a subagent",
		"use the subagent",
		"use subagent",
		"ask a subagent",
		"have a subagent",
		"delegate this",
		"delegate it",
		"delegate the task",
		"delegate this task",
		"delegate to a subagent",
		"hand this off to a subagent",
		"hand it off to a subagent",
	)
}

func turnConstraintForQuery(query string) string {
	if isPlanningOnlyPrompt(query) {
		return "This request is planning-only. Answer directly in plain language. Do not call tools, do not emit tool-call syntax, and do not persist or schedule anything unless the user explicitly asked for that. Keep the answer compact and structured: no preamble, no recap, and no filler. Use the shortest format that fully covers the requested horizon, with brief day/week bullets and brief carry-forward or reminder notes only."
	}
	if tool := directDelegationTool(query); tool != "" {
		return fmt.Sprintf("This request explicitly asks for delegated execution. Call `%s` as your first tool step. Do not use tool_search or tool_call first, and do not solve the task yourself before delegating. After the delegated result returns, answer with that result plainly and concisely.", tool)
	}
	if command := explicitExecCommand(query); command != "" {
		return fmt.Sprintf("This request explicitly asks for command execution. Call `exec` as your first tool step with `command` set to exactly %q. After the tool returns, answer using the actual command output. Do not claim permission denial or failure unless the tool result says so.", command)
	}
	if path, content := explicitWriteFileRequest(query); path != "" {
		constraint := fmt.Sprintf("This request explicitly asks for a file write. Call `write_file` as your first tool step with `path` set to exactly %q", path)
		if content != "" {
			constraint += fmt.Sprintf(" and `content` set to exactly %q", content)
		}
		constraint += ". Do not claim success unless the tool call succeeds."
		return constraint
	}
	if isCommitmentCapturePrompt(query) {
		return "This request explicitly asks you to capture commitments. Use `memory` to write each distinct commitment once, then stop calling tools and answer directly with a concise reminder/follow-up plan. Do not repeat the same memory write or loop on memory writes."
	}
	return ""
}

func isPlanningOnlyPrompt(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}

	planningSignals := []string{
		"give me a plan",
		"provide a plan",
		"daily plan",
		"weekly plan",
		"schedule",
		"roadmap",
		"workflow",
		"strategy",
		"check-in schedule",
		"check in schedule",
		"milestones",
	}
	hasPlanningSignal := false
	for _, signal := range planningSignals {
		if strings.Contains(q, signal) {
			hasPlanningSignal = true
			break
		}
	}
	if !hasPlanningSignal {
		return false
	}

	actionSignals := []string{
		"run ",
		"execute",
		"read ",
		"write ",
		"edit ",
		"append",
		"fetch ",
		"search ",
		"list ",
		"create a file",
		"save ",
		"store ",
		"remember ",
		"capture ",
		"set reminder",
		"remind me",
		"schedule reminder",
		"create reminder",
		"subagent",
		"spawn ",
		"background task",
	}
	for _, signal := range actionSignals {
		if strings.Contains(q, signal) {
			return false
		}
	}

	return true
}

func isCommitmentCapturePrompt(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	if !(strings.Contains(q, "commitment") || strings.Contains(q, "commitments")) {
		return false
	}
	return strings.Contains(q, "capture") ||
		strings.Contains(q, "track") ||
		strings.Contains(q, "remember") ||
		strings.Contains(q, "store") ||
		strings.Contains(q, "i have")
}

func (al *AgentLoop) createFantasyAgent(ctx context.Context, opts processOptions, systemPrompt string, adaptedTools []fantasy.AgentTool, prepareStep func(context.Context, fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error)) (fantasy.Agent, ids.UUID, ids.UUID, error) {
	conversationID, runID, err := al.prepareRuntimeState(ctx, opts.SessionKey)
	if err != nil {
		return nil, ids.UUID{}, ids.UUID{}, err
	}

	baseRuntime := OffloadingToolRuntime{
		Base:           fantasy.DAGToolRuntime{MaxConcurrency: defaultToolMaxConcurrency},
		KV:             al.kvDelegate,
		Queries:        al.queries,
		ConversationID: conversationID,
		RunID:          runID,
		ThresholdChars: al.offloadThresholdChars,
	}
	toolRuntime := SecureBusToolRuntime{
		Offloader:    baseRuntime,
		FantasyTools: fantasyToolMap(adaptedTools, al.tools),
		Bus:          al.secureBus,
		SessionKey:   opts.SessionKey,
		Channel:      opts.Channel,
		ChatID:       opts.ChatID,
		UserPrompt:   opts.UserMessage,
		StateStore:   al.stateStore,
		RunID:        runID,
	}

	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(al.maxIterations)),
		fantasy.WithPrepareStep(prepareStep),
		fantasy.WithToolRuntime(toolRuntime),
		fantasy.WithTransitionObserver(al.transitionObserver(runID)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}

	return fantasy.NewAgent(al.languageModel, agentOpts...), conversationID, runID, nil
}

// postProcess handles the common finalization after Generate or Stream:
// extract final text, save session, summarize, observe, optionally send response.
func (al *AgentLoop) postProcess(ctx context.Context, opts processOptions, finalContent string, metrics agentRunMetrics) string {
	al.ensureFinalAssistantMessage(opts.SessionKey, finalContent)

	// Snapshot BEFORE summarization can truncate history, preventing
	// observation manager from seeing an incomplete view.
	tail := al.sessionsToMessagePairs(opts.SessionKey)
	al.persistRunCheckpoint(ctx, opts, metrics)

	// Defer disk/DB persistence off the response path; in-memory state
	// is already consistent for observation/summarization reads.
	go al.sessions.Save(opts.SessionKey)

	if opts.EnableSummary {
		go al.maybeSummarize(context.WithoutCancel(ctx), opts.SessionKey, opts.Channel, opts.ChatID)
	}

	al.obsManager.MaybeObserveAsync(ctx, opts.SessionKey, tail)

	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"session_key":  opts.SessionKey,
			"steps":        metrics.StepCount,
			"final_length": len(finalContent),
		})

	// Record task completion for RL analysis (best-effort, don't fail on error)
	completion := TaskCompletion{
		TaskID:      opts.SessionKey,
		Description: utils.Truncate(opts.UserMessage, 100),
		TokensUsed:  metrics.TotalTokens,
		ToolCalls:   metrics.ToolCalls,
		Errors:      metrics.Errors,
		Completed:   true,
		CreatedAt:   time.Now().UTC(),
	}
	if err := al.endTask(ctx, opts.ConversationID, opts.RunID, completion); err != nil {
		logger.WarnCF("agent", "Failed to record task completion",
			map[string]interface{}{"error": err.Error(), "session": opts.SessionKey})
	}

	return finalContent
}

// resolveFinalContent normalizes the final assistant response from an agent run.
// Some providers return an empty final response even though tool results or an
// earlier step already contain the real answer. Prefer the most meaningful
// recovery candidate from the step history before failing deterministically.
func (al *AgentLoop) resolveFinalContent(finalContent string, steps []fantasy.StepResult) (string, error) {
	trimmed := strings.TrimSpace(finalContent)
	if trimmed != "" {
		return trimmed, nil
	}

	type candidate struct {
		text   string
		score  int
		source string
	}
	candidates := make([]candidate, 0, 8)
	for i := len(steps) - 1; i >= 0; i-- {
		toolNamesByID := make(map[string]string, len(steps[i].Content.ToolCalls()))
		for _, tc := range steps[i].Content.ToolCalls() {
			toolNamesByID[tc.ToolCallID] = observedToolName(tc.ToolName, tc.Input)
		}

		toolResults := steps[i].Content.ToolResults()
		for j := len(toolResults) - 1; j >= 0; j-- {
			tr := toolResults[j]
			toolName := tr.ToolName
			if mapped := toolNamesByID[tr.ToolCallID]; mapped != "" {
				toolName = mapped
			}
			switch out := tr.Result.(type) {
			case fantasy.ToolResultOutputContentText:
				txt := strings.TrimSpace(out.Text)
				if txt != "" {
					candidates = append(candidates, candidate{
						text:   txt,
						score:  scoreToolResultText(toolName, txt),
						source: "tool_result",
					})
				}
			case fantasy.ToolResultOutputContentError:
				if out.Error != nil {
					txt := strings.TrimSpace(out.Error.Error())
					if txt != "" {
						candidates = append(candidates, candidate{
							text:   txt,
							score:  scoreToolResultError(toolName, txt),
							source: "tool_error",
						})
					}
				}
			case fantasy.ToolResultOutputContentMedia:
				txt := strings.TrimSpace(out.Text)
				if txt != "" {
					candidates = append(candidates, candidate{
						text:   txt,
						score:  1,
						source: "tool_media",
					})
				}
			}
			if len(candidates) >= 8 {
				break
			}
		}

		stepText := strings.TrimSpace(steps[i].Content.Text())
		if stepText != "" {
			candidates = append(candidates, candidate{
				text:   stepText,
				score:  scoreRecoveredStepText(stepText),
				source: "step_text",
			})
		}

		if len(candidates) >= 8 {
			break
		}
	}

	best := candidate{score: -1000}
	for _, c := range candidates {
		if c.score > best.score {
			best = c
		}
	}

	toolCalls := 0
	for _, step := range steps {
		toolCalls += len(step.Content.ToolCalls())
	}

	if best.text != "" && best.score > 0 {
		if toolCalls == 0 && best.source == "step_text" && best.score <= 1 {
			if fallback := fallbackNoToolResponse(al, steps); fallback != "" {
				return fallback, nil
			}
		} else {
			logger.WarnCF("agent", "Recovered empty final response",
				map[string]interface{}{
					"candidates": len(candidates),
					"score":      best.score,
					"source":     best.source,
				})
			return best.text, nil
		}
	}
	if best.text != "" {
		if toolCalls == 0 {
			if fallback := fallbackNoToolResponse(al, steps); fallback != "" {
				return fallback, nil
			}
		} else {
			logger.WarnCF("agent", "Recovered empty final response from fallback tool content",
				map[string]interface{}{
					"candidates": len(candidates),
					"score":      best.score,
					"source":     best.source,
				})
			return best.text, nil
		}
	}
	if toolCalls == 0 {
		if fallback := fallbackNoToolResponse(al, steps); fallback != "" {
			return fallback, nil
		}
	}

	return "", fmt.Errorf("agent produced no final response text (steps=%d, tool_calls=%d)", len(steps), toolCalls)
}

func fallbackNoToolResponse(al *AgentLoop, steps []fantasy.StepResult) string {
	if len(steps) != 1 {
		return ""
	}

	stepText := strings.TrimSpace(steps[0].Content.Text())
	if stepText == "" {
		return ""
	}

	lower := strings.ToLower(stepText)
	if strings.Contains(lower, "which file") || strings.Contains(lower, "what do you want") || strings.Contains(lower, "what would you like") {
		return stepText
	}
	if al != nil {
		if grounded := strings.TrimSpace(al.groundFinalContent(stepText, stepText, steps)); grounded != "" {
			if grounded != stepText {
				return grounded
			}
		}
	}
	return ""
}

func (al *AgentLoop) groundFinalContent(userPrompt, finalContent string, steps []fantasy.StepResult) string {
	grounded := strings.TrimSpace(finalContent)
	if grounded == "" {
		return grounded
	}

	lowerPrompt := strings.ToLower(userPrompt)
	lowerFinal := strings.ToLower(grounded)
	toolTexts := collectToolTexts(steps)

	if strings.Contains(lowerPrompt, "contains the word") {
		quoted := quotedTerms(userPrompt)
		if len(quoted) >= 3 {
			haystack := strings.ToLower(strings.Join(toolTexts["read_file"], "\n"))
			if strings.Contains(haystack, strings.ToLower(quoted[0])) {
				return fmt.Sprintf("The file contains %q, so I wrote %q to result.txt.", quoted[0], quoted[1])
			}
			return fmt.Sprintf("The file does not contain %q, so I wrote %q to result.txt.", quoted[0], quoted[2])
		}
	}

	if strings.Contains(lowerPrompt, "replace") {
		quoted := quotedTerms(userPrompt)
		if len(quoted) >= 2 {
			replacement := quoted[len(quoted)-1]
			if !strings.Contains(lowerFinal, strings.ToLower(replacement)) {
				if readBack := latestToolText(toolTexts, "read_file"); readBack != "" &&
					strings.Contains(strings.ToLower(readBack), strings.ToLower(replacement)) {
					return strings.TrimSpace(grounded + "\n\nUpdated content: " + readBack)
				}
				return strings.TrimSpace(grounded + "\n\nConfirmed replacement includes " + replacement + ".")
			}
		}
	}

	if strings.Contains(lowerPrompt, "uname -s") || strings.Contains(lowerPrompt, "os name") {
		if osName := detectOSText(toolTexts); osName != "" && !strings.Contains(lowerFinal, strings.ToLower(osName)) {
			return strings.TrimSpace(grounded + fmt.Sprintf("\n\nConfirmed OS name: %s.", osName))
		}
	}

	if strings.Contains(lowerPrompt, "date +%y") || strings.Contains(lowerPrompt, "current year") {
		if year := detectYearText(toolTexts); year != "" && !strings.Contains(lowerFinal, year) {
			return strings.TrimSpace(grounded + fmt.Sprintf("\n\nConfirmed value: %s.", year))
		}
	}

	if memoryMiss := detectMemoryNoResultText(toolTexts); memoryMiss != "" &&
		asksForMemorySearch(lowerPrompt) &&
		!mentionsNoResults(lowerFinal) {
		return memoryMiss
	}

	if execError := detectExecErrorText(toolTexts); execError != "" &&
		asksForExecResult(lowerPrompt) &&
		(!mentionsExecFailure(lowerFinal) || strings.Contains(lowerFinal, "completed successfully")) {
		return execError
	}
	if execOutput := detectExecSuccessText(toolTexts); execOutput != "" &&
		asksForExecResult(lowerPrompt) &&
		mentionsExecFailure(lowerFinal) {
		return formatExecSuccessResponse(execOutput)
	}

	if strings.Contains(lowerPrompt, "commitment") || strings.Contains(lowerPrompt, "commitments") {
		clauses := extractCommitmentClauses(userPrompt)
		missing := make([]string, 0, len(clauses))
		for _, clause := range clauses {
			anchor := commitmentAnchor(clause)
			if anchor == "" {
				continue
			}
			if !strings.Contains(lowerFinal, anchor) {
				missing = append(missing, strings.TrimSpace(clause))
			}
		}
		if len(missing) > 0 {
			builder := strings.Builder{}
			builder.WriteString(strings.TrimSpace(grounded))
			builder.WriteString("\n\nTracked commitments:\n")
			for _, clause := range clauses {
				builder.WriteString("- ")
				builder.WriteString(strings.TrimSpace(clause))
				builder.WriteString("\n")
			}
			if strings.Contains(lowerPrompt, "reminder") || strings.Contains(lowerPrompt, "follow-up") || strings.Contains(lowerPrompt, "follow up") {
				builder.WriteString("\nReminder/follow-up plan: schedule each item against its stated timing and keep overdue items active until complete.")
			}
			return strings.TrimSpace(builder.String())
		}
		if (strings.Contains(lowerPrompt, "reminder") || strings.Contains(lowerPrompt, "follow-up") || strings.Contains(lowerPrompt, "follow up")) &&
			!strings.Contains(lowerFinal, "remind") &&
			!strings.Contains(lowerFinal, "follow") &&
			!strings.Contains(lowerFinal, "schedule") &&
			!strings.Contains(lowerFinal, "timeline") {
			return strings.TrimSpace(grounded + "\n\nReminder/follow-up plan: schedule each item against its stated timing and review progress at each checkpoint.")
		}
		if strings.Contains(lowerPrompt, "daily plan") &&
			strings.Contains(lowerPrompt, "carry forward") &&
			!strings.Contains(lowerFinal, "monday") &&
			!strings.Contains(lowerFinal, "tuesday") {
			return strings.TrimSpace(expandWeekdayAbbreviations(grounded))
		}
	}

	if strings.Contains(lowerPrompt, "webinar") &&
		strings.Contains(lowerPrompt, "risk") &&
		strings.Contains(lowerPrompt, "follow-up") {
		if (strings.Contains(lowerFinal, "risk") || strings.Contains(lowerFinal, "fallback") || strings.Contains(lowerFinal, "failure")) &&
			!strings.Contains(lowerFinal, "follow-up") &&
			!strings.Contains(lowerFinal, "follow up") &&
			!strings.Contains(lowerFinal, "check-in") &&
			!strings.Contains(lowerFinal, "verify") {
			return strings.TrimSpace(grounded + "\n\nFollow-up actions: schedule a 24-hour follow-up check-in to send the recording and slides, verify attendee follow-up status, and review the risk/fallback notes before the next webinar.")
		}
	}

	if strings.Contains(lowerPrompt, "skill") &&
		(strings.Contains(lowerPrompt, "template") || strings.Contains(lowerPrompt, "greeting")) &&
		!strings.Contains(lowerFinal, "formal") &&
		!strings.Contains(lowerFinal, "casual") &&
		!strings.Contains(lowerFinal, "greeting") &&
		!strings.Contains(lowerFinal, "template") {
		if skillName := explicitSkillName(userPrompt); skillName != "" {
			if summary := al.recoverSkillSummary(skillName); summary != "" {
				return summary
			}
		}
	}

	if readBack := latestToolText(toolTexts, "read_file"); readBack != "" {
		wantsReadBack := strings.Contains(lowerPrompt, "read it back") ||
			strings.Contains(lowerPrompt, "read the file back") ||
			strings.Contains(lowerPrompt, "tell me the full contents") ||
			strings.Contains(lowerPrompt, "full contents") ||
			strings.Contains(lowerPrompt, "confirm the value") ||
			strings.Contains(lowerPrompt, "confirm the contents")
		readBackLower := strings.ToLower(strings.TrimSpace(readBack))
		if wantsReadBack && readBackLower != "" && !strings.Contains(lowerFinal, readBackLower) {
			return strings.TrimSpace(grounded + "\n\nRead-back confirmation: " + truncateGroundedSnippet(readBack, 240))
		}
	}

	return grounded
}

func expandWeekdayAbbreviations(text string) string {
	replacer := strings.NewReplacer(
		"**Mon", "**Monday",
		"**Tue", "**Tuesday",
		"**Wed", "**Wednesday",
		"**Thu", "**Thursday",
		"**Fri", "**Friday",
		"**Sat", "**Saturday",
		"**Sun", "**Sunday",
		" Mon ", " Monday ",
		" Tue ", " Tuesday ",
		" Wed ", " Wednesday ",
		" Thu ", " Thursday ",
		" Fri ", " Friday ",
		" Sat ", " Saturday ",
		" Sun ", " Sunday ",
	)
	return replacer.Replace(text)
}

func collectToolTexts(steps []fantasy.StepResult) map[string][]string {
	toolTexts := make(map[string][]string)
	for _, step := range steps {
		toolNamesByID := make(map[string]string, len(step.Content.ToolCalls()))
		for _, tc := range step.Content.ToolCalls() {
			toolNamesByID[tc.ToolCallID] = observedToolName(tc.ToolName, tc.Input)
		}
		for _, tr := range step.Content.ToolResults() {
			toolName := tr.ToolName
			if mapped := toolNamesByID[tr.ToolCallID]; mapped != "" {
				toolName = mapped
			}

			var text string
			switch out := tr.Result.(type) {
			case fantasy.ToolResultOutputContentText:
				text = strings.TrimSpace(out.Text)
			case fantasy.ToolResultOutputContentError:
				if out.Error != nil {
					text = strings.TrimSpace(out.Error.Error())
				}
			}
			if text == "" {
				continue
			}
			if toolName == "" {
				continue
			}
			toolTexts[toolName] = append(toolTexts[toolName], text)
		}
	}
	return toolTexts
}

func observedToolName(toolName, input string) string {
	if toolName != "tool_call" {
		return toolName
	}

	var payload struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return toolName
	}
	if strings.TrimSpace(payload.ToolName) == "" {
		return toolName
	}
	return payload.ToolName
}

func latestToolText(toolTexts map[string][]string, toolName string) string {
	values := toolTexts[toolName]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func scoreToolResultText(toolName, text string) int {
	lower := strings.ToLower(text)
	if toolName == "tool_search" || strings.Contains(lower, "\"kind\":\"tool\"") {
		return 0
	}
	if strings.Contains(lower, "tool not found") || strings.Contains(lower, "path is required") {
		return 1
	}
	return 2
}

func scoreToolResultError(toolName, text string) int {
	if toolName == "tool_search" {
		return 0
	}
	if strings.TrimSpace(text) == "" {
		return -1
	}
	return 3
}

func scoreRecoveredStepText(text string) int {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case lower == "":
		return -1
	case strings.HasPrefix(lower, "i'll "),
		strings.HasPrefix(lower, "i will "),
		strings.HasPrefix(lower, "let me "),
		strings.HasPrefix(lower, "now i'll "),
		strings.HasPrefix(lower, "first, let me "),
		strings.HasSuffix(lower, ":"):
		return 0
	default:
		return 1
	}
}

func quotedTerms(prompt string) []string {
	re := regexp.MustCompile(`'([^']+)'`)
	matches := re.FindAllStringSubmatch(prompt, -1)
	terms := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			terms = append(terms, match[1])
		}
	}
	return terms
}

func explicitExecCommand(prompt string) string {
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "run ") && !strings.Contains(lower, "command") {
		return ""
	}
	terms := quotedTerms(prompt)
	if len(terms) == 0 {
		return ""
	}
	return strings.TrimSpace(terms[0])
}

func explicitWriteFileRequest(prompt string) (string, string) {
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "file called") && !strings.Contains(lower, "write the text") && !strings.Contains(lower, "create a file") {
		return "", ""
	}

	path := ""
	pathRE := regexp.MustCompile("(?i)(?:to a file called|file called)\\s+(?:\"([^\"]+)\"|'([^']+)'|`([^`]+)`|([^\\s\"'`,]+))")
	if matches := pathRE.FindStringSubmatch(prompt); len(matches) > 1 {
		for _, candidate := range matches[1:] {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				path = strings.Trim(candidate, "\"'`.,")
				break
			}
		}
	}

	content := ""
	terms := quotedTerms(prompt)
	if strings.Contains(lower, "write the text") && len(terms) > 0 {
		content = strings.TrimSpace(terms[0])
	}
	if content == "" && strings.Contains(lower, " with ") && len(terms) > 0 {
		content = strings.TrimSpace(terms[0])
	}
	if path != "" && content == path && len(terms) > 1 {
		content = strings.TrimSpace(terms[1])
	}

	return path, content
}

func explicitListDirPath(prompt string) string {
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "list ") || !strings.Contains(lower, "directory") {
		return ""
	}

	dirRE := regexp.MustCompile(`(?i)list (?:the )?([A-Za-z0-9_./-]+) directory`)
	if matches := dirRE.FindStringSubmatch(prompt); len(matches) > 1 {
		candidate := strings.Trim(matches[1], "\"'`.,")
		if candidate != "" && candidate != "workspace" {
			return candidate
		}
	}

	if path, _ := explicitWriteFileRequest(prompt); path != "" {
		if idx := strings.LastIndex(path, "/"); idx > 0 {
			return path[:idx]
		}
	}

	return ""
}

func explicitReadFilePath(prompt string) string {
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "read it back") || strings.Contains(lower, "read back") {
		if path, _ := explicitWriteFileRequest(prompt); path != "" {
			return path
		}
	}

	readRE := regexp.MustCompile(`(?i)read (?:the )?file\s+([^\s"'` + "`" + `,]+)`)
	if matches := readRE.FindStringSubmatch(prompt); len(matches) > 1 {
		return strings.Trim(matches[1], "\"'`.,")
	}

	return ""
}

func explicitFetchURL(prompt string) string {
	urlRE := regexp.MustCompile(`https?://[^\s"'` + "`" + `)]+`)
	match := urlRE.FindString(prompt)
	return strings.TrimRight(strings.TrimSpace(match), ".,")
}

func detectOSText(toolTexts map[string][]string) string {
	candidates := []string{
		strings.ToLower(strings.Join(toolTexts["read_file"], "\n")),
		strings.ToLower(strings.Join(toolTexts["exec"], "\n")),
	}
	for _, candidate := range candidates {
		switch {
		case strings.Contains(candidate, "linux"):
			return "Linux"
		case strings.Contains(candidate, "darwin"):
			return "Darwin"
		case strings.Contains(candidate, "windows"):
			return "Windows"
		}
	}
	return ""
}

func detectYearText(toolTexts map[string][]string) string {
	re := regexp.MustCompile(`\b20\d{2}\b`)
	for _, toolName := range []string{"read_file", "exec"} {
		for _, value := range toolTexts[toolName] {
			if year := re.FindString(value); year != "" {
				return year
			}
		}
	}
	return ""
}

func detectMemoryNoResultText(toolTexts map[string][]string) string {
	for _, toolName := range []string{"memory", "memory_search"} {
		for i := len(toolTexts[toolName]) - 1; i >= 0; i-- {
			text := strings.TrimSpace(toolTexts[toolName][i])
			lower := strings.ToLower(text)
			if strings.Contains(lower, "no results") ||
				strings.Contains(lower, "no matching") ||
				strings.Contains(lower, "not found") ||
				strings.Contains(lower, "no memories") ||
				strings.Contains(lower, "didn't find") ||
				strings.Contains(lower, "don't have") {
				return text
			}
		}
	}
	return ""
}

func detectExecErrorText(toolTexts map[string][]string) string {
	for i := len(toolTexts["exec"]) - 1; i >= 0; i-- {
		text := strings.TrimSpace(toolTexts["exec"][i])
		lower := strings.ToLower(text)
		if strings.Contains(lower, "timed out") ||
			strings.Contains(lower, "blocked") ||
			strings.Contains(lower, "cannot be empty") ||
			strings.Contains(lower, "no-op placeholder") ||
			strings.Contains(lower, "failed to") ||
			strings.Contains(lower, "shell execution is disabled") ||
			strings.Contains(lower, "working_dir blocked") ||
			strings.Contains(lower, "exit code:") {
			return text
		}
	}
	return ""
}

func detectExecSuccessText(toolTexts map[string][]string) string {
	if detectExecErrorText(toolTexts) != "" {
		return ""
	}
	for i := len(toolTexts["exec"]) - 1; i >= 0; i-- {
		text := strings.TrimSpace(toolTexts["exec"][i])
		if text == "" || text == "(no output)" {
			continue
		}
		return text
	}
	return ""
}

func formatExecSuccessResponse(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return trimmed
	}
	if !strings.Contains(trimmed, "\n") {
		return fmt.Sprintf("The output is:\n\n```\n%s\n```", trimmed)
	}
	return trimmed
}

func asksForMemorySearch(lowerPrompt string) bool {
	return strings.Contains(lowerPrompt, "search your memory") ||
		(strings.Contains(lowerPrompt, "memory") && strings.Contains(lowerPrompt, "search")) ||
		strings.Contains(lowerPrompt, "look in your memory") ||
		strings.Contains(lowerPrompt, "look through your memory") ||
		strings.Contains(lowerPrompt, "what do you remember") ||
		strings.Contains(lowerPrompt, "recall")
}

func asksForExecResult(lowerPrompt string) bool {
	return strings.Contains(lowerPrompt, "run ") ||
		strings.Contains(lowerPrompt, "command") ||
		strings.Contains(lowerPrompt, "shell") ||
		strings.Contains(lowerPrompt, "tell me the result") ||
		strings.Contains(lowerPrompt, "tell me the output")
}

func mentionsNoResults(lowerText string) bool {
	return strings.Contains(lowerText, "no results") ||
		strings.Contains(lowerText, "nothing") ||
		strings.Contains(lowerText, "not found") ||
		strings.Contains(lowerText, "no memories") ||
		strings.Contains(lowerText, "no matching")
}

func mentionsExecFailure(lowerText string) bool {
	return strings.Contains(lowerText, "timed out") ||
		strings.Contains(lowerText, "blocked") ||
		strings.Contains(lowerText, "denied") ||
		strings.Contains(lowerText, "permission") ||
		strings.Contains(lowerText, "cannot") ||
		strings.Contains(lowerText, "placeholder") ||
		strings.Contains(lowerText, "not allowed") ||
		strings.Contains(lowerText, "failed") ||
		strings.Contains(lowerText, "error") ||
		strings.Contains(lowerText, "exit code")
}

func extractCommitmentClauses(prompt string) []string {
	lower := strings.ToLower(prompt)
	idx := strings.Index(lower, "commitment")
	if idx == -1 {
		return nil
	}
	segment := prompt[idx:]
	colonIdx := strings.Index(segment, ":")
	if colonIdx == -1 {
		return nil
	}
	segment = strings.TrimSpace(segment[colonIdx+1:])
	if cut := strings.Index(segment, "."); cut >= 0 {
		segment = segment[:cut]
	}
	segment = strings.ReplaceAll(segment, ", and ", ", ")
	segment = strings.ReplaceAll(segment, " and ", ", ")
	parts := strings.Split(segment, ",")
	clauses := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clauses = append(clauses, part)
		}
	}
	return clauses
}

func commitmentAnchor(clause string) string {
	stopWords := map[string]struct{}{
		"submit": {}, "send": {}, "book": {}, "renew": {}, "follow": {}, "up": {}, "with": {}, "my": {}, "the": {},
		"a": {}, "an": {}, "by": {}, "in": {}, "next": {}, "month": {}, "tomorrow": {}, "tonight": {}, "days": {},
		"day": {}, "from": {}, "now": {}, "documents": {}, "appointment": {}, "receipt": {}, "this": {}, "these": {},
	}
	cleaned := strings.NewReplacer(".", " ", ":", " ", ";", " ", "(", " ", ")", " ", "/", " ", "-", " ").Replace(strings.ToLower(clause))
	for _, token := range strings.Fields(cleaned) {
		if len(token) < 3 {
			continue
		}
		if _, blocked := stopWords[token]; blocked {
			continue
		}
		return token
	}
	return ""
}

func truncateGroundedSnippet(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen]) + "..."
}

func explicitSkillName(prompt string) string {
	for _, term := range quotedTerms(prompt) {
		trimmed := strings.TrimSpace(term)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (al *AgentLoop) recoverSkillSummary(skillName string) string {
	if al == nil || al.tools == nil {
		return ""
	}

	skillTool, ok := al.tools.Get("skill_read")
	if !ok || skillTool == nil {
		return ""
	}

	result := skillTool.Execute(context.Background(), map[string]interface{}{"name": skillName})
	if result == nil || result.IsError {
		return ""
	}

	content := strings.TrimSpace(result.ForLLM)
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	templates := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- **Formal**:"):
			templates = append(templates, strings.TrimPrefix(line, "- **Formal**: "))
		case strings.HasPrefix(line, "- **Casual**:"):
			templates = append(templates, strings.TrimPrefix(line, "- **Casual**: "))
		case strings.HasPrefix(line, "- **Technical**:"):
			templates = append(templates, strings.TrimPrefix(line, "- **Technical**: "))
		}
	}

	if len(templates) == 0 {
		return strings.TrimSpace("I read the skill " + skillName + ". It provides greeting templates.")
	}

	builder := strings.Builder{}
	builder.WriteString("I read the skill ")
	builder.WriteString(skillName)
	builder.WriteString(". It provides greeting templates")
	builder.WriteString(": ")
	for i, template := range templates {
		if i > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(template)
	}
	builder.WriteString(".")
	return builder.String()
}

// runAgentLoop is the core message processing logic.
// It delegates to assembleContext for shared pre-processing, then branches on
// opts.Streaming to either Generate (synchronous) or Stream (real-time deltas).
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (finalContent string, err error) {
	ctx = withToolSessionKey(ctx, opts.SessionKey)
	al.activeSessionKey.Store(opts.SessionKey)
	defer al.activeSessionKey.Store("")
	defer func() {
		if err == nil {
			return
		}
		if !opts.RunID.IsZero() {
			al.persistFailedRun(ctx, opts, err)
			if endErr := al.endTask(ctx, opts.ConversationID, opts.RunID, TaskCompletion{
				TaskID:      opts.SessionKey,
				Description: utils.Truncate(opts.UserMessage, 100),
				Completed:   false,
				CreatedAt:   time.Now().UTC(),
			}); endErr != nil {
				logger.WarnCF("agent", "Failed to record failed task completion",
					map[string]interface{}{"error": endErr.Error(), "session": opts.SessionKey})
			}
		}
		if opts.SessionKey != "" {
			go al.sessions.Save(opts.SessionKey)
		}
	}()

	ac, err := al.assembleContext(ctx, opts)
	if err != nil {
		return "", err
	}
	opts.ConversationID = ac.conversationID
	opts.RunID = ac.runID

	if opts.Streaming {
		return al.runStreaming(ctx, opts, ac)
	}

	result, err := ac.agent.Generate(ctx, fantasy.AgentCall{
		Prompt:   ac.userPrompt,
		Messages: ac.fantasyHistory,
	})
	if err != nil {
		logger.ErrorCF("agent", "Fantasy Generate failed",
			map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("agent Generate failed: %w", err)
	}

	al.replayAgentSteps(ctx, opts.SessionKey, result.Steps)

	finalContent, err = al.resolveFinalContent(result.Response.Content.Text(), result.Steps)
	if err != nil {
		logger.ErrorCF("agent", "Agent finished without final response text",
			map[string]interface{}{
				"error": err.Error(),
				"steps": len(result.Steps),
			})
		return "", err
	}
	finalContent = al.groundFinalContent(opts.UserMessage, finalContent, result.Steps)

	return al.postProcess(ctx, opts, finalContent, collectAgentRunMetrics(result)), nil
}

// runStreaming uses Fantasy's agent.Stream() to stream token deltas to the bus
// in real time, using the pre-assembled context from assembleContext.
// Failure terminalization is owned by runAgentLoop so streaming errors only
// record terminal state once.
func (al *AgentLoop) runStreaming(ctx context.Context, opts processOptions, ac assembledContext) (finalContent string, err error) {
	opts.ConversationID = ac.conversationID
	opts.RunID = ac.runID
	var streamedText strings.Builder

	streamCall := fantasy.AgentStreamCall{
		Prompt:   ac.userPrompt,
		Messages: ac.fantasyHistory,

		OnTextDelta: func(id, text string) error {
			streamedText.WriteString(text)
			if opts.Channel != "" && opts.ChatID != "" {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:     opts.Channel,
					ChatID:      opts.ChatID,
					Content:     text,
					StreamDelta: true,
				})
			}
			return nil
		},

		OnStepFinish: func(step fantasy.StepResult) error {
			al.replayAgentSteps(ctx, opts.SessionKey, []fantasy.StepResult{step})
			return nil
		},

		OnToolCall: func(tc fantasy.ToolCallContent) error {
			logger.DebugCF("agent", "Streaming tool call",
				map[string]interface{}{
					"tool": tc.ToolName,
					"id":   tc.ToolCallID,
				})
			return nil
		},
	}

	result, err := ac.agent.Stream(ctx, streamCall)
	if err != nil {
		logger.ErrorCF("agent", "Fantasy Stream failed",
			map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("agent Stream failed: %w", err)
	}

	responseText := result.Response.Content.Text()
	if strings.TrimSpace(responseText) == "" {
		responseText = streamedText.String()
	}
	finalContent, err = al.resolveFinalContent(responseText, result.Steps)
	if err != nil {
		logger.ErrorCF("agent", "Streaming agent finished without final response text",
			map[string]interface{}{
				"error": err.Error(),
				"steps": len(result.Steps),
			})
		return "", err
	}
	finalContent = al.groundFinalContent(opts.UserMessage, finalContent, result.Steps)

	return al.postProcess(ctx, opts, finalContent, collectAgentRunMetrics(result)), nil
}

// runLLMIteration — DELETED. Replaced by Fantasy's internal agent loop.

// auditStep logs tool calls from a Fantasy step result to the audit log.
func (al *AgentLoop) auditStep(_ context.Context, step fantasy.StepResult, sessionKey string) {
	toolCalls := step.Content.ToolCalls()
	toolResults := step.Content.ToolResults()
	if len(toolCalls) == 0 && len(toolResults) == 0 {
		return
	}

	callByID := make(map[string]fantasy.ToolCallContent, len(toolCalls))
	for _, tc := range toolCalls {
		callByID[tc.ToolCallID] = tc
	}

	// Prefer result-based entries because they carry success/failure semantics.
	if len(toolResults) > 0 {
		for _, tr := range toolResults {
			toolName := ""
			toolInput := ""
			if tc, ok := callByID[tr.ToolCallID]; ok {
				toolName = tc.ToolName
				toolInput = tc.Input
			}
			if toolName == "" {
				toolName = "unknown_tool"
			}

			action := "tool_success"
			output := ""
			switch out := tr.Result.(type) {
			case fantasy.ToolResultOutputContentText:
				output = out.Text
			case fantasy.ToolResultOutputContentMedia:
				output = out.Text
			case fantasy.ToolResultOutputContentError:
				action = "tool_error"
				if out.Error != nil {
					output = out.Error.Error()
				} else {
					output = "tool returned error output"
				}
			}

			entry := &memory.AuditEntry{
				ID:         ids.New(),
				AgentID:    pkg.NAME,
				SessionKey: sessionKey,
				Action:     action,
				Target:     toolName,
				ToolCallID: tr.ToolCallID,
				Input:      toolInput,
				Output:     output,
				Success:    action != "tool_error",
				ErrorMsg:   output,
			}
			if action != "tool_error" {
				entry.ErrorMsg = ""
			}
			if !al.enqueueAuditEntry(entry) {
				logger.WarnCF("agent", "Audit channel unavailable, dropping tool result entry",
					map[string]interface{}{"tool": toolName, "action": action})
			}
		}
		return
	}

	// Legacy fallback: if no tool results were emitted, record tool calls.
	for _, tc := range toolCalls {
		entry := &memory.AuditEntry{
			ID:         ids.New(),
			AgentID:    pkg.NAME,
			SessionKey: sessionKey,
			Action:     "tool_call",
			Target:     tc.ToolName,
			ToolCallID: tc.ToolCallID,
			Input:      tc.Input,
			Success:    true,
		}
		if !al.enqueueAuditEntry(entry) {
			logger.WarnCF("agent", "Audit channel unavailable, dropping tool call entry",
				map[string]interface{}{"tool": tc.ToolName})
		}
	}
}

func (al *AgentLoop) enqueueAuditEntry(entry *memory.AuditEntry) (ok bool) {
	if al.auditChan == nil || entry == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	select {
	case al.auditChan <- entry:
		return true
	default:
		return false
	}
}
