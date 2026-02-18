// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	fantasy "charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	picofantasy "github.com/sipeed/picoclaw/pkg/fantasy"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory/delegate"
	memstore "github.com/sipeed/picoclaw/pkg/memory/store"
	"github.com/sipeed/picoclaw/pkg/messages"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/state"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type AgentLoop struct {
	bus               *bus.MessageBus
	languageModel     fantasy.LanguageModel
	workspace         string
	model             string
	contextWindow     int // Maximum context window size in tokens
	maxIterations     int
	sessions          *session.SessionManager
	state             *state.Manager
	contextBuilder    *ContextBuilder
	tools             *tools.ToolRegistry
	memoryStore       *memstore.MemoryStore // 3-tier MemGPT memory (nil if init failed)
	running           atomic.Bool
	summarizing       sync.Map       // Tracks which sessions are currently being summarized
	summarizeFailures sync.Map       // Tracks consecutive summarization failures per session (string -> int)
	cfg               *config.Config // Stored for subagent factory access
	channelManager    *channels.Manager
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	UserMessage     string // User message content (may include prefix)
	DefaultResponse string // Response when LLM returns empty
	EnableSummary   bool   // Whether to trigger summarization
	SendResponse    bool   // Whether to send response via bus
	NoHistory       bool   // If true, don't load session history (for heartbeat)
	Streaming       bool   // If true, stream token deltas to bus via OnTextDelta
}

// createToolRegistry creates a tool registry with common tools.
// This is shared between main agent and subagents.
func createToolRegistry(workspace string, restrict bool, cfg *config.Config, msgBus *bus.MessageBus) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()

	// File system tools
	registry.Register(tools.NewReadFileTool(workspace, restrict))
	registry.Register(tools.NewWriteFileTool(workspace, restrict))
	registry.Register(tools.NewListDirTool(workspace, restrict))
	registry.Register(tools.NewEditFileTool(workspace, restrict))
	registry.Register(tools.NewAppendFileTool(workspace, restrict))

	// Shell execution
	registry.Register(tools.NewExecTool(workspace, restrict))

	if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
		PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
	}); searchTool != nil {
		registry.Register(searchTool)
	}
	registry.Register(tools.NewWebFetchTool(50000))

	// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
	registry.Register(tools.NewI2CTool())
	registry.Register(tools.NewSPITool())

	// Message tool - available to both agent and subagent
	// Subagent uses it to communicate directly with user
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
		return nil
	})
	registry.Register(messageTool)

	return registry
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, model fantasy.LanguageModel) *AgentLoop {
	workspace := cfg.WorkspacePath()
	os.MkdirAll(workspace, 0755)

	restrict := cfg.Agents.Defaults.RestrictToWorkspace

	// Create tool registry for main agent
	toolsRegistry := createToolRegistry(workspace, restrict, cfg, msgBus)

	// Create subagent manager with its own tool registry
	subagentManager := tools.NewSubagentManager(model, cfg.Agents.Defaults.Model, workspace, msgBus)
	subagentTools := createToolRegistry(workspace, restrict, cfg, msgBus)
	// Subagent doesn't need spawn/subagent tools to avoid recursion
	subagentManager.SetTools(subagentTools)

	// Register spawn tool (for main agent)
	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	// Register subagent tool (synchronous execution)
	subagentTool := tools.NewSubagentTool(subagentManager)
	toolsRegistry.Register(subagentTool)

	sessionsManager := session.NewSessionManager(filepath.Join(workspace, "sessions"))

	// Create state manager for atomic state persistence
	stateManager := state.NewManager(workspace)

	// Create context builder and set tools registry
	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	// Initialize 3-tier MemGPT memory system
	var ms *memstore.MemoryStore
	memDBPath := filepath.Join(workspace, "memory", "picoclaw.db")
	os.MkdirAll(filepath.Dir(memDBPath), 0755)

	del, err := delegate.NewLibSQLDelegate(memDBPath)
	if err != nil {
		logger.WarnCF("agent", "Failed to create memory delegate, memory system disabled",
			map[string]interface{}{"error": err.Error()})
	} else {
		if err := del.Init(context.Background()); err != nil {
			logger.WarnCF("agent", "Failed to init memory schema, memory system disabled",
				map[string]interface{}{"error": err.Error()})
			del.Close()
		} else {
			chunker := memstore.NewMarkdownChunker(memstore.DefaultMarkdownChunkerConfig())
			ms = memstore.New(del, chunker, nil, memstore.Config{
				ContextWindowTokens:    cfg.Agents.Defaults.MaxTokens,
				OffloadThresholdTokens: 4000,
			})
			contextBuilder.SetMemoryStore(ms)

			// Register the memory tool
			memTool := NewMemGPTTool(ms, "picoclaw", "default")
			toolsRegistry.Register(memTool)

			logger.InfoCF("agent", "3-tier memory system initialized",
				map[string]interface{}{"db_path": memDBPath})
		}
	}

	// Register meta-tools for progressive disclosure (tool_search + tool_call)
	toolsRegistry.RegisterMetaTools()

	// If memory tool is a gateway, mark it visible in progressive mode
	if ms != nil {
		toolsRegistry.MarkGateway("memory")
	}

	// Apply progressive disclosure config
	if cfg.Tools.ProgressiveDisclosure {
		toolsRegistry.SetProgressiveDisclosure(true)
		logger.InfoCF("agent", "Progressive tool disclosure enabled",
			map[string]interface{}{"gateway_tools": toolsRegistry.ListVisible()})
	}

	return &AgentLoop{
		bus:            msgBus,
		languageModel:  model,
		workspace:      workspace,
		model:          cfg.Agents.Defaults.Model,
		contextWindow:  cfg.Agents.Defaults.MaxTokens,
		maxIterations:  cfg.Agents.Defaults.MaxToolIterations,
		sessions:       sessionsManager,
		state:          stateManager,
		contextBuilder: contextBuilder,
		tools:          toolsRegistry,
		memoryStore:    ms,
		summarizing:    sync.Map{},
		cfg:            cfg,
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			response, err := al.processMessage(ctx, msg)
			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				// Check if the message tool already sent a response during this round.
				// If so, skip publishing to avoid duplicate messages to the user.
				alreadySent := false
				if tool, ok := al.tools.Get("message"); ok {
					if mt, ok := tool.(*tools.MessageTool); ok {
						alreadySent = mt.HasSentInRound()
					}
				}

				if !alreadySent {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					})
				}
			}
		}
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
	if al.memoryStore != nil {
		al.memoryStore.Close()
	}
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessage(ctx, msg)
}

// ProcessDirectStreaming processes a message with streaming token delivery.
// Text deltas are published to the bus as StreamDelta messages in real time.
func (al *AgentLoop) ProcessDirectStreaming(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "user",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
		Streaming:       true,
	})
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Check for commands
	if response, handled := al.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// Process as user message
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
	} else {
		// Fallback
		originChannel = "cli"
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]interface{}{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Agent acts as dispatcher only - subagent handles user interaction via message tool
	// Don't forward result here, subagent should use message tool to communicate with user
	logger.InfoCF("agent", "Subagent completed",
		map[string]interface{}{
			"sender_id":   msg.SenderID,
			"channel":     originChannel,
			"content_len": len(content),
		})

	// Agent only logs, does not respond to user
	return "", nil
}

// runAgentLoop is the core message processing logic.
// It handles context building, Fantasy agent creation, tool execution, and response handling.
// When opts.Streaming is true, delegates to runAgentLoopStreaming for real-time token delivery.
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (string, error) {
	if opts.Streaming {
		return al.runAgentLoopStreaming(ctx, opts)
	}
	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	al.updateToolContexts(opts.Channel, opts.ChatID)

	// 2. Build messages (skip history for heartbeat)
	var history []messages.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}
	builtMsgs := al.contextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		nil,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Split built messages into system prompt, conversation history, and current user prompt.
	// BuildMessages returns: [system, ...history, user]
	systemPrompt := ""
	var historyMsgs []messages.Message
	userPrompt := opts.UserMessage

	if len(builtMsgs) > 0 && builtMsgs[0].Role == "system" {
		systemPrompt = builtMsgs[0].Content
		// History is everything between system and last user message.
		if len(builtMsgs) > 2 {
			historyMsgs = builtMsgs[1 : len(builtMsgs)-1]
		}
	}

	// 5. Convert history to Fantasy message format
	fantasyHistory := picofantasy.MessagesToFantasy(historyMsgs)

	// 6. Build adapted tools from PicoClaw registry (with optional offloading)
	adaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   al.memoryStore,
		AgentID:    "picoclaw",
		SessionKey: opts.SessionKey,
	}
	adaptedTools := picofantasy.BuildAdaptedTools(al.tools, al.bus, opts.Channel, opts.ChatID, adaptCfg)

	// 7. Create Fantasy agent with tools and configuration
	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(al.maxIterations)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}
	agent := fantasy.NewAgent(al.languageModel, agentOpts...)

	logger.DebugCF("agent", "Fantasy agent created",
		map[string]interface{}{
			"model":          al.model,
			"tools_count":    len(adaptedTools),
			"history_count":  len(historyMsgs),
			"max_iterations": al.maxIterations,
			"memory_enabled": al.memoryStore != nil,
		})

	// 8. Call Fantasy agent.Generate()
	result, err := agent.Generate(ctx, fantasy.AgentCall{
		Prompt:   userPrompt,
		Messages: fantasyHistory,
	})
	if err != nil {
		logger.ErrorCF("agent", "Fantasy Generate failed",
			map[string]interface{}{
				"error": err.Error(),
			})
		return "", fmt.Errorf("agent Generate failed: %w", err)
	}

	// 9. Save all step messages to session
	stepCount := len(result.Steps)
	for _, step := range result.Steps {
		stepMsgs := picofantasy.StepToMessages(step)
		for _, m := range stepMsgs {
			al.sessions.AddFullMessage(opts.SessionKey, m)
		}
	}

	// 10. Extract final text
	finalContent := result.Response.Content.Text()

	// 11. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 12. Save session
	al.sessions.Save(opts.SessionKey)

	// 13. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey, opts.Channel, opts.ChatID)
	}

	// 14. Optional: send response via bus
	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 15. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"session_key":  opts.SessionKey,
			"steps":        stepCount,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// runAgentLoopStreaming uses Fantasy's agent.Stream() to stream token deltas
// to the bus in real time. Structure mirrors runAgentLoop but uses AgentStreamCall
// with OnTextDelta, OnStepFinish, and OnToolCall callbacks.
func (al *AgentLoop) runAgentLoopStreaming(ctx context.Context, opts processOptions) (string, error) {
	// 0. Record last channel
	if opts.Channel != "" && opts.ChatID != "" {
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	al.updateToolContexts(opts.Channel, opts.ChatID)

	// 2. Build messages
	var history []messages.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}
	builtMsgs := al.contextBuilder.BuildMessages(history, summary, opts.UserMessage, nil, opts.Channel, opts.ChatID)

	// 3. Save user message
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Split into system/history/user
	systemPrompt := ""
	var historyMsgs []messages.Message
	userPrompt := opts.UserMessage

	if len(builtMsgs) > 0 && builtMsgs[0].Role == "system" {
		systemPrompt = builtMsgs[0].Content
		if len(builtMsgs) > 2 {
			historyMsgs = builtMsgs[1 : len(builtMsgs)-1]
		}
	}

	// 5. Convert history
	fantasyHistory := picofantasy.MessagesToFantasy(historyMsgs)

	// 6. Build adapted tools (with optional offloading)
	streamAdaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   al.memoryStore,
		AgentID:    "picoclaw",
		SessionKey: opts.SessionKey,
	}
	adaptedTools := picofantasy.BuildAdaptedTools(al.tools, al.bus, opts.Channel, opts.ChatID, streamAdaptCfg)

	// 7. Create Fantasy agent
	agentOpts := []fantasy.AgentOption{
		fantasy.WithTools(adaptedTools...),
		fantasy.WithStopConditions(fantasy.StepCountIs(al.maxIterations)),
	}
	if systemPrompt != "" {
		agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
	}
	fantasyAgent := fantasy.NewAgent(al.languageModel, agentOpts...)

	logger.DebugCF("agent", "Fantasy streaming agent created",
		map[string]interface{}{
			"model":          al.model,
			"tools_count":    len(adaptedTools),
			"history_count":  len(historyMsgs),
			"max_iterations": al.maxIterations,
			"memory_enabled": al.memoryStore != nil,
		})

	// 8. Build streaming call with callbacks
	streamCall := fantasy.AgentStreamCall{
		Prompt:   userPrompt,
		Messages: fantasyHistory,

		// Stream text deltas to bus in real time
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

		// Save each step's messages to session as they complete
		OnStepFinish: func(step fantasy.StepResult) error {
			stepMsgs := picofantasy.StepToMessages(step)
			for _, m := range stepMsgs {
				al.sessions.AddFullMessage(opts.SessionKey, m)
			}
			return nil
		},

		// Log tool calls as they happen
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			logger.DebugCF("agent", "Streaming tool call",
				map[string]interface{}{
					"tool": tc.ToolName,
					"id":   tc.ToolCallID,
				})
			return nil
		},
	}

	// 9. Call Fantasy agent.Stream()
	result, err := fantasyAgent.Stream(ctx, streamCall)
	if err != nil {
		logger.ErrorCF("agent", "Fantasy Stream failed",
			map[string]interface{}{"error": err.Error()})
		return "", fmt.Errorf("agent Stream failed: %w", err)
	}

	// 10. Extract final text
	finalContent := result.Response.Content.Text()
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 11. Save session
	al.sessions.Save(opts.SessionKey)

	// 12. Summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey, opts.Channel, opts.ChatID)
	}

	// 13. Log response
	stepCount := len(result.Steps)
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Streaming response: %s", responsePreview),
		map[string]interface{}{
			"session_key":  opts.SessionKey,
			"steps":        stepCount,
			"final_length": len(finalContent),
			"total_tokens": result.TotalUsage.TotalTokens,
		})

	return finalContent, nil
}

// runLLMIteration — DELETED. Replaced by Fantasy's internal agent loop.

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

// maybeSummarize triggers summarization if the session history exceeds thresholds.
func (al *AgentLoop) maybeSummarize(sessionKey, channel, chatID string) {
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	threshold := al.contextWindow * 75 / 100

	if len(newHistory) > 20 || tokenEstimate > threshold {
		if _, loading := al.summarizing.LoadOrStore(sessionKey, true); !loading {
			go func() {
				defer al.summarizing.Delete(sessionKey)
				// Notify user about optimization if not an internal channel
				if !constants.IsInternalChannel(channel) {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: channel,
						ChatID:  chatID,
						Content: "⚠️ Memory threshold reached. Optimizing conversation history...",
					})
				}
				al.summarizeSession(sessionKey)
			}()
		}
	}
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest 50% of messages (keeping system prompt and last user message).
func (al *AgentLoop) forceCompression(sessionKey string) {
	history := al.sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// Keep system prompt (usually [0]) and the very last message (user's trigger)
	// We want to drop the oldest half of the *conversation*
	// Assuming [0] is system, [1:] is conversation
	conversation := history[1 : len(history)-1]
	if len(conversation) == 0 {
		return
	}

	// Helper to find the mid-point of the conversation
	mid := len(conversation) / 2

	// New history structure:
	// 1. System Prompt
	// 2. [Summary of dropped part] - synthesized
	// 3. Second half of conversation
	// 4. Last message

	// Simplified approach for emergency: Drop first half of conversation
	// and rely on existing summary if present, or create a placeholder.

	droppedCount := mid
	keptConversation := conversation[mid:]

	newHistory := make([]messages.Message, 0)
	newHistory = append(newHistory, history[0]) // System prompt

	// Add a note about compression
	compressionNote := fmt.Sprintf("[System: Emergency compression dropped %d oldest messages due to context limit]", droppedCount)
	// If there was an existing summary, we might lose it if it was in the dropped part (which is just messages).
	// The summary is stored separately in session.Summary, so it persists!
	// We just need to ensure the user knows there's a gap.

	// We only modify the messages list here
	newHistory = append(newHistory, messages.Message{
		Role:    "system",
		Content: compressionNote,
	})

	newHistory = append(newHistory, keptConversation...)
	newHistory = append(newHistory, history[len(history)-1]) // Last message

	// Update session
	al.sessions.SetHistory(sessionKey, newHistory)
	al.sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]interface{}{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(newHistory),
	})
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Tools info
	tools := al.tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(tools),
		"names": tools,
	}

	// Skills info
	info["skills"] = al.contextBuilder.GetSkillsInfo()

	return info
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(msgs []messages.Message) string {
	if len(msgs) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, msg := range msgs {
		result += fmt.Sprintf("  [%d] Role: %s\n", i, msg.Role)
		if len(msg.ToolCalls) > 0 {
			result += "  ToolCalls:\n"
			for _, tc := range msg.ToolCalls {
				result += fmt.Sprintf("    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					result += fmt.Sprintf("      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			result += fmt.Sprintf("  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			result += fmt.Sprintf("  ToolCallID: %s\n", msg.ToolCallID)
		}
		result += "\n"
	}
	result += "]"
	return result
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := al.sessions.GetHistory(sessionKey)
	summary := al.sessions.GetSummary(sessionKey)

	// Keep last 4 messages for continuity
	if len(history) <= 4 {
		return
	}

	toSummarize := history[:len(history)-4]

	// Oversized Message Guard
	// Skip messages larger than 50% of context window to prevent summarizer overflow
	maxMessageTokens := al.contextWindow / 2
	validMessages := make([]messages.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgTokens := len(m.Content) / 2
		if msgTokens > maxMessageTokens {
			omitted = true
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
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		al.sessions.SetSummary(sessionKey, finalSummary)
		al.sessions.TruncateHistory(sessionKey, 4)
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
		const emergencyKeep = 10
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
	prompt := "Provide a concise summary of this conversation segment, preserving core context and key points.\n"
	if existingSummary != "" {
		prompt += "Existing context: " + existingSummary + "\n"
	}
	prompt += "\nCONVERSATION:\n"
	for _, m := range batch {
		prompt += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	return al.callModel(ctx, prompt)
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

// estimateTokens estimates the number of tokens in a message list.
func (al *AgentLoop) estimateTokens(msgs []messages.Message) int {
	totalChars := 0
	for _, m := range msgs {
		totalChars += utf8.RuneCountInString(m.Content)
	}
	return totalChars * 2 / 5
}

func (al *AgentLoop) handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return "", false
	}

	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", false
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "/show":
		if len(args) < 1 {
			return "Usage: /show [model|channel]", true
		}
		switch args[0] {
		case "model":
			return fmt.Sprintf("Current model: %s", al.model), true
		case "channel":
			return fmt.Sprintf("Current channel: %s", msg.Channel), true
		default:
			return fmt.Sprintf("Unknown show target: %s", args[0]), true
		}

	case "/list":
		if len(args) < 1 {
			return "Usage: /list [models|channels]", true
		}
		switch args[0] {
		case "models":
			// TODO: Fetch available models dynamically if possible
			return "Available models: glm-4.7, claude-3-5-sonnet, gpt-4o (configured in config.json/env)", true
		case "channels":
			if al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			channels := al.channelManager.GetEnabledChannels()
			if len(channels) == 0 {
				return "No channels enabled", true
			}
			return fmt.Sprintf("Enabled channels: %s", strings.Join(channels, ", ")), true
		default:
			return fmt.Sprintf("Unknown list target: %s", args[0]), true
		}

	case "/switch":
		if len(args) < 3 || args[1] != "to" {
			return "Usage: /switch [model|channel] to <name>", true
		}
		target := args[0]
		value := args[2]

		switch target {
		case "model":
			oldModel := al.model
			al.model = value
			return fmt.Sprintf("Switched model from %s to %s", oldModel, value), true
		case "channel":
			// This changes the 'default' channel for some operations, or effectively redirects output?
			// For now, let's just validate if the channel exists
			if al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			if _, exists := al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Sprintf("Channel '%s' not found or not enabled", value), true
			}

			// If message came from CLI, maybe we want to redirect CLI output to this channel?
			// That would require state persistence about "redirected channel"
			// For now, just acknowledged.
			return fmt.Sprintf("Switched target channel to %s (Note: this currently only validates existence)", value), true
		default:
			return fmt.Sprintf("Unknown switch target: %s", target), true
		}
	}

	return "", false
}
