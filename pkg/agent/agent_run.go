// assembledContext holds the pre-processed context produced by assembleContext,
// consumed by both the Generate and Stream code paths.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	picofantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
)

type assembledContext struct {
	systemPrompt   string
	userPrompt     string
	fantasyHistory []fantasy.Message
	adaptedTools   []fantasy.AgentTool
	agent          fantasy.Agent
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
		conversationID = cached.(ids.UUID)
	} else {
		al.conversationMu.Lock()
		defer al.conversationMu.Unlock()
		if cached, ok := al.conversationIDs.Load(sessionKey); ok {
			conversationID = cached.(ids.UUID)
		} else {
			conversationID = ids.New()
			title := sessionKey
			if _, err := al.queries.CreateAgentConversation(ctx, memsqlc.CreateAgentConversationParams{
				ID:    conversationID,
				Title: &title,
			}); err != nil {
				return ids.UUID{}, ids.UUID{}, fmt.Errorf("create agent conversation: %w", err)
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
	builtMsgs := al.buildPromptMessages(opts, history, summary)
	systemPrompt, historyMsgs, userPrompt := al.splitMessages(opts, builtMsgs)

	logger.DebugCF("agent", "assembleContext: history messages",
		map[string]interface{}{
			"history": formatMessagesForLog(historyMsgs),
		})

	fantasyHistory := picofantasy.MessagesToFantasy(historyMsgs)
	adaptedTools, prepareStep := al.prepareToolset(ctx, opts)
	agent, err := al.createFantasyAgent(ctx, opts, systemPrompt, adaptedTools, prepareStep)
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

func (al *AgentLoop) refreshContextBlocks(ctx context.Context, opts processOptions) {
	al.updateToolContexts(opts.Channel, opts.ChatID)

	block := al.obsManager.LoadBlock(ctx, opts.SessionKey)
	al.contextBuilder.SetObservationBlock(block)

	kb := tools.LoadKnowledgeBlock(ctx, al.memDelegate, opts.SessionKey)
	al.contextBuilder.SetKnowledgeBlock(kb)

	if al.identitySync != nil {
		_ = al.identitySync.CheckAndSync(ctx)
	}
}

func (al *AgentLoop) loadSessionState(ctx context.Context, opts processOptions) ([]messages.Message, string) {
	var history []messages.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}

	return al.applyDAGCompression(ctx, opts.SessionKey, history), summary
}

func (al *AgentLoop) buildPromptMessages(opts processOptions, history []messages.Message, summary string) []messages.Message {
	builtMsgs := al.contextBuilder.BuildMessages(history, summary, opts.UserMessage, nil, opts.Channel, opts.ChatID)
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)
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
	adaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   al.memoryStore,
		AgentID:    pkg.NAME,
		SessionKey: opts.SessionKey,
	}

	adaptedTools := picofantasy.BuildAdaptedTools(al.tools, al.bus, opts.Channel, opts.ChatID, adaptCfg)
	if al.toolResultSearch != nil {
		adaptedTools = append(adaptedTools, al.toolResultSearch)
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

		newAdapted := picofantasy.AdaptTools(newTools, msgBus, channel, chatID, adaptCfg)
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

func (al *AgentLoop) createFantasyAgent(ctx context.Context, opts processOptions, systemPrompt string, adaptedTools []fantasy.AgentTool, prepareStep func(context.Context, fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error)) (fantasy.Agent, error) {
	conversationID, runID, err := al.prepareRuntimeState(ctx, opts.SessionKey)
	if err != nil {
		return nil, err
	}

	baseRuntime := OffloadingToolRuntime{
		Base:           fantasy.DAGToolRuntime{MaxConcurrency: defaultToolMaxConcurrency},
		KV:             al.kvDelegate,
		Queries:        al.queries,
		ConversationID: conversationID,
		RunID:          runID,
	}
	toolRuntime := SecureBusToolRuntime{
		Base:       baseRuntime,
		Bus:        al.secureBus,
		SessionKey: opts.SessionKey,
		StateStore: al.stateStore,
		RunID:      runID,
	}

	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(al.maxIterations)),
		fantasy.WithPrepareStep(prepareStep),
		fantasy.WithToolRuntime(toolRuntime),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}

	return fantasy.NewAgent(al.languageModel, agentOpts...), nil
}

// postProcess handles the common finalization after Generate or Stream:
// extract final text, save session, summarize, observe, optionally send response.
func (al *AgentLoop) postProcess(ctx context.Context, opts processOptions, finalContent string, stepCount int) string {
	al.sessions.Save(opts.SessionKey)

	if opts.EnableSummary {
		al.maybeSummarize(ctx, opts.SessionKey, opts.Channel, opts.ChatID)
	}

	tail := al.sessionsToMessagePairs(opts.SessionKey)
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
			"steps":        stepCount,
			"final_length": len(finalContent),
		})

	return finalContent
}

// resolveFinalContent normalizes the final assistant response from an agent run.
// Some providers return an empty final response even though an earlier step
// already produced text. In that case, recover the latest non-empty text from
// steps. If no text exists at all, return a deterministic error.
func (al *AgentLoop) resolveFinalContent(finalContent string, steps []fantasy.StepResult) (string, error) {
	trimmed := strings.TrimSpace(finalContent)
	if trimmed != "" {
		return trimmed, nil
	}

	for i := len(steps) - 1; i >= 0; i-- {
		stepText := strings.TrimSpace(steps[i].Content.Text())
		if stepText != "" {
			logger.WarnCF("agent", "Recovered empty final response from prior step text",
				map[string]interface{}{
					"step_index": i,
				})
			return stepText, nil
		}
	}

	type candidate struct {
		text  string
		score int
	}
	candidates := make([]candidate, 0, 8)
	for i := len(steps) - 1; i >= 0; i-- {
		toolResults := steps[i].Content.ToolResults()
		for j := len(toolResults) - 1; j >= 0; j-- {
			tr := toolResults[j]
			switch out := tr.Result.(type) {
			case fantasy.ToolResultOutputContentText:
				txt := strings.TrimSpace(out.Text)
				if txt != "" {
					score := 2
					if tr.ToolName == "tool_search" || strings.Contains(strings.ToLower(txt), "\"kind\":\"tool\"") {
						score = 0
					}
					if strings.Contains(strings.ToLower(txt), "tool not found") ||
						strings.Contains(strings.ToLower(txt), "path is required") {
						score = -1
					}
					candidates = append(candidates, candidate{text: txt, score: score})
				}
			case fantasy.ToolResultOutputContentError:
				if out.Error != nil {
					txt := strings.TrimSpace(out.Error.Error())
					if txt != "" {
						candidates = append(candidates, candidate{text: txt, score: -1})
					}
				}
			case fantasy.ToolResultOutputContentMedia:
				txt := strings.TrimSpace(out.Text)
				if txt != "" {
					candidates = append(candidates, candidate{text: txt, score: 1})
				}
			}
			if len(candidates) >= 8 {
				break
			}
		}
		if len(candidates) >= 8 {
			break
		}
	}

	bestText := ""
	bestScore := -1000
	for _, c := range candidates {
		if c.score > bestScore {
			bestScore = c.score
			bestText = c.text
		}
	}

	if bestText != "" && bestScore > 0 {
		logger.WarnCF("agent", "Recovered empty final response from tool results",
			map[string]interface{}{
				"candidates": len(candidates),
				"score":      bestScore,
			})
		return bestText, nil
	}

	toolCalls := 0
	for _, step := range steps {
		toolCalls += len(step.Content.ToolCalls())
	}

	return "", fmt.Errorf("agent produced no final response text (steps=%d, tool_calls=%d)", len(steps), toolCalls)
}

// runAgentLoop is the core message processing logic.
// It delegates to assembleContext for shared pre-processing, then branches on
// opts.Streaming to either Generate (synchronous) or Stream (real-time deltas).
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (string, error) {
	al.activeSessionKey.Store(opts.SessionKey)

	ac, err := al.assembleContext(ctx, opts)
	if err != nil {
		return "", err
	}

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

	for _, step := range result.Steps {
		stepMsgs := picofantasy.StepToMessages(step)
		for _, m := range stepMsgs {
			al.sessions.AddFullMessage(opts.SessionKey, m)
		}
		al.auditStep(ctx, step, opts.SessionKey)
	}

	finalContent, err := al.resolveFinalContent(result.Response.Content.Text(), result.Steps)
	if err != nil {
		logger.ErrorCF("agent", "Agent finished without final response text",
			map[string]interface{}{
				"error": err.Error(),
				"steps": len(result.Steps),
			})
		return "", err
	}
	return al.postProcess(ctx, opts, finalContent, len(result.Steps)), nil
}

// runStreaming uses Fantasy's agent.Stream() to stream token deltas to the bus
// in real time, using the pre-assembled context from assembleContext.
func (al *AgentLoop) runStreaming(ctx context.Context, opts processOptions, ac assembledContext) (string, error) {
	streamCall := fantasy.AgentStreamCall{
		Prompt:   ac.userPrompt,
		Messages: ac.fantasyHistory,

		OnTextDelta: func(id, text string) error {
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
			stepMsgs := picofantasy.StepToMessages(step)
			for _, m := range stepMsgs {
				al.sessions.AddFullMessage(opts.SessionKey, m)
			}
			al.auditStep(ctx, step, opts.SessionKey)
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

	finalContent, err := al.resolveFinalContent(result.Response.Content.Text(), result.Steps)
	if err != nil {
		logger.ErrorCF("agent", "Streaming agent finished without final response text",
			map[string]interface{}{
				"error": err.Error(),
				"steps": len(result.Steps),
			})
		return "", err
	}
	return al.postProcess(ctx, opts, finalContent, len(result.Steps)), nil
}

// runLLMIteration — DELETED. Replaced by Fantasy's internal agent loop.

// auditStep logs tool calls from a Fantasy step result to the audit log.
func (al *AgentLoop) auditStep(ctx context.Context, step fantasy.StepResult, sessionKey string) {
	toolCalls := step.Content.ToolCalls()
	if len(toolCalls) == 0 {
		return
	}

	for _, tc := range toolCalls {
		entry := &memory.AuditEntry{
			ID:         ids.New(),
			AgentID:    pkg.NAME,
			SessionKey: sessionKey,
			Action:     "tool_call",
			Target:     tc.ToolName,
			Input:      tc.Input,
		}
		aCtx, cancel := context.WithTimeout(ctx, time.Second)
		if err := al.memDelegate.InsertAuditEntry(aCtx, entry); err != nil {
			logger.WarnCF("agent", "Failed to log audit entry",
				map[string]interface{}{"tool": tc.ToolName, "error": err.Error()})
		}
		cancel()
	}
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (al *AgentLoop) updateToolContexts(channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := al.tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}
