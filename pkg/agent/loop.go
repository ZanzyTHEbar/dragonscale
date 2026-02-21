// DragonScale - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

// FIXME: This file is a mess, we need to clean it up and make it more readable and maintainable
// Break this into modules with single responsibility and composability in mind
// Leverage Ports and Adapters pattern to achieve this (where boundaries are defined sensibly)
// - AgentLoop: Main agent loop and orchestrator
// - ContextBuilder: Builds the context for the agent
// - Tools: Tool registry and management
// - Memory: Memory store and management
// - State: State management
// - Session: Session management
// - Identity: Identity management
// - SecureBus: SecureBus management

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/channels"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	picofantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/dag"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/session"
	"github.com/ZanzyTHEbar/dragonscale/pkg/state"
	picosync "github.com/ZanzyTHEbar/dragonscale/pkg/sync"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
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
	memoryStore       *memstore.MemoryStore  // 3-tier MemGPT memory (always initialized)
	memDelegate       memory.MemoryDelegate  // DB delegate (always initialized)
	obsManager        *observation.Manager   // Observational memory (always initialized)
	secureBus         *securebus.Bus         // ITR SecureBus (always initialized)
	queries           *memsqlc.Queries       // SQL query surface for runtime persistence
	kvDelegate        KVDelegate             // KV adapter for offloaded tool results
	stateStore        *StateStore            // Agent run state persistence
	conversationIDs   sync.Map               // map[sessionKey]ids.UUID
	conversationMu    sync.Mutex             // serializes conversation creation path
	identitySync      *picosync.IdentitySync // File→DB sync for identity docs (nil if memory disabled)
	activeSessionKey  atomic.Value           // Current session key for tool access
	running           atomic.Bool
	summarizing       sync.Map       // Tracks which sessions are currently being summarized
	summarizeFailures sync.Map       // Tracks consecutive summarization failures per session (string -> int)
	cfg               *config.Config // Stored for subagent factory access
	channelManager    *channels.Manager
	toolResultSearch  fantasy.AgentTool
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey    string // Session identifier for history/context
	Channel       string // Target channel for tool execution
	ChatID        string // Target chat ID for tool execution
	SenderID      string // Originating sender identifier (for logging/audit)
	UserMessage   string // User message content (may include prefix)
	EnableSummary bool   // Whether to trigger summarization
	SendResponse  bool   // Whether to send response via bus
	NoHistory     bool   // If true, don't load session history (for heartbeat)
	Streaming     bool   // If true, stream token deltas to bus via OnTextDelta
}

func initSecretStore() (*security.SecretStore, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}

	secretsPath := filepath.Join(cfgDir, "secrets.json")
	keyring := security.NewEnvKeyring(security.MasterKeyEnvVar)
	ss, err := security.NewSecretStore(secretsPath, keyring)
	if err != nil {
		return nil, fmt.Errorf("initialize secret store: %w", err)
	}

	if os.Getenv(security.MasterKeyEnvVar) == "" {
		logger.WarnCF("security", "master key env var is not set; secret injection requiring stored secrets will fail",
			map[string]interface{}{"env_var": security.MasterKeyEnvVar})
	}
	return ss, nil
}

// createToolRegistry creates a tool registry with common tools.
// createToolRegistry builds the base tool set (filesystem, shell, web, etc.).
// Parent and subagent registries start from the same base; memory/search/skill
// tools are registered separately on each so they have isolated discovery state.
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

func NewAgentLoop(ctx context.Context, cfg *config.Config, msgBus *bus.MessageBus, model fantasy.LanguageModel) (*AgentLoop, error) {
	sandbox := cfg.SandboxPath()
	os.MkdirAll(sandbox, 0755)

	workspace := sandbox
	restrict := cfg.RestrictToSandbox()

	toolsRegistry := createToolRegistry(workspace, restrict, cfg, msgBus)

	subagentManager := tools.NewSubagentManager(model, cfg.Agents.Defaults.Model, workspace, msgBus)
	subagentTools := createToolRegistry(workspace, restrict, cfg, msgBus)
	subagentManager.SetTools(subagentTools)

	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	subagentTool := tools.NewSubagentTool(subagentManager)
	toolsRegistry.Register(subagentTool)
	agenticMapTool := tools.NewAgenticMapTool(subagentManager)

	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)
	contextBuilder.SetContextWindow(cfg.Agents.Defaults.MaxTokens)

	sl := contextBuilder.SkillsLoader()
	toolsRegistry.Register(tools.NewSkillSearchTool(sl))
	toolsRegistry.Register(tools.NewSkillReadTool(sl))
	toolsRegistry.Register(tools.NewSkillTraverseTool(sl))

	// Initialize 3-tier MemGPT memory system (always enabled, fail-fast on error)
	memDBPath := cfg.DBPath()
	os.MkdirAll(filepath.Dir(memDBPath), 0755)

	del, err := delegate.NewFromConfig(cfg.Memory, memDBPath)
	if err != nil {
		return nil, fmt.Errorf("memory delegate init: %w", err)
	}
	if err := del.Init(ctx); err != nil {
		del.Close()
		return nil, fmt.Errorf("memory schema init: %w", err)
	}
	memDelegate := del
	queries := memDelegate.Queries()
	if queries == nil {
		del.Close()
		return nil, fmt.Errorf("memory delegate queries are not initialized")
	}
	kv := NewDelegateKV(memDelegate, "dragonscale")
	stateStore := NewStateStore(queries)

	offloadThreshold := cfg.Memory.OffloadThresholdTokens
	if offloadThreshold <= 0 {
		// Default to ~3% of the context window, floored at 2048 tokens.
		// Small windows (4K-8K) get proportionally tighter thresholds;
		// large windows (128K+) avoid wasteful offloading of short results.
		offloadThreshold = max(cfg.Agents.Defaults.MaxTokens*3/100, 2048)
	}
	chunker := memstore.NewMarkdownChunker(memstore.DefaultMarkdownChunkerConfig())

	embedder, embErr := memstore.NewEmbedderFromConfig(cfg.Memory.Embedding, cfg.Providers)
	if embErr != nil {
		logger.WarnCF("agent", "Failed to create embedding provider, archival search will use FTS5 only",
			map[string]interface{}{"error": embErr.Error()})
	}

	ms := memstore.New(del, chunker, embedder, memstore.Config{
		ContextWindowTokens:    cfg.Agents.Defaults.MaxTokens,
		OffloadThresholdTokens: offloadThreshold,
	})
	contextBuilder.SetMemoryStore(ms)

	memTool := NewMemGPTTool(ms, "dragonscale", "default")
	toolsRegistry.Register(memTool)
	toolsRegistry.Register(tools.NewObligationTool(memDelegate, "dragonscale"))

	toolsRegistry.Register(tools.NewKeywordSearchTool(ms, "dragonscale"))
	toolsRegistry.Register(tools.NewSemanticSearchTool(ms, "dragonscale"))
	toolsRegistry.Register(tools.NewChunkReadTool(ms, "dragonscale"))
	mapRuntime := tools.NewMapRuntime(queries, "dragonscale", model, cfg.Agents.Defaults.Model, subagentManager)
	agenticMapTool.SetRuntime(mapRuntime)
	toolsRegistry.Register(agenticMapTool)
	llmMapTool := tools.NewLLMMapTool(model, cfg.Agents.Defaults.Model)
	llmMapTool.SetRuntime(mapRuntime)
	toolsRegistry.Register(llmMapTool)
	toolsRegistry.Register(tools.NewMapRunStatusTool(mapRuntime))
	toolsRegistry.Register(tools.NewMapRunReadTool(mapRuntime))

	// Register memory/search/skill tools on subagent registry so spawned
	// agents can search knowledge, offload results, and use skills.
	subagentTools.Register(NewMemGPTTool(ms, "dragonscale", "default"))
	subagentTools.Register(tools.NewObligationTool(memDelegate, "dragonscale"))
	subagentTools.Register(tools.NewKeywordSearchTool(ms, "dragonscale"))
	subagentTools.Register(tools.NewSemanticSearchTool(ms, "dragonscale"))
	subagentTools.Register(tools.NewChunkReadTool(ms, "dragonscale"))
	subagentTools.Register(tools.NewSkillSearchTool(sl))
	subagentTools.Register(tools.NewSkillReadTool(sl))
	subagentTools.RegisterMetaTools()
	for _, name := range []string{"memory", "read_file", "write_file", "list_dir", "exec"} {
		subagentTools.MarkGateway(name)
	}

	contextBuilder.SetDelegate(del)

	if migErr := memory.MigrateState(ctx, workspace, del, "dragonscale"); migErr != nil {
		logger.WarnCF("agent", "State KV migration failed (non-fatal)",
			map[string]interface{}{"error": migErr.Error()})
	}
	if migErr := memory.MigrateDocuments(ctx, workspace, del, "dragonscale"); migErr != nil {
		logger.WarnCF("agent", "Document migration failed (non-fatal)",
			map[string]interface{}{"error": migErr.Error()})
	}
	if migErr := memory.MigrateLongTermMemory(ctx, workspace, del, "dragonscale"); migErr != nil {
		logger.WarnCF("agent", "Long-term memory migration failed (non-fatal)",
			map[string]interface{}{"error": migErr.Error()})
	}
	if migErr := memory.MigrateDailyNotes(ctx, workspace, del, "dragonscale"); migErr != nil {
		logger.WarnCF("agent", "Daily notes migration failed (non-fatal)",
			map[string]interface{}{"error": migErr.Error()})
	}

	// Identity file sync (disk → DB)
	var idSync *picosync.IdentitySync
	identityDir, idErr := config.IdentityDir()
	if idErr != nil {
		logger.WarnCF("agent", "Could not resolve identity dir, identity sync disabled",
			map[string]interface{}{"error": idErr.Error()})
	} else {
		idSync = picosync.New(identityDir, "dragonscale", memDelegate)
		if syncErr := idSync.SyncAll(ctx); syncErr != nil {
			logger.WarnCF("agent", "Initial identity sync failed (non-fatal)",
				map[string]interface{}{"error": syncErr.Error()})
		} else {
			logger.InfoCF("agent", "Identity files synced to DB", nil)
		}
		if watchErr := idSync.Watch(ctx); watchErr != nil {
			logger.WarnCF("agent", "Identity file watcher failed to start, using mtime fallback",
				map[string]interface{}{"error": watchErr.Error()})
		}
	}

	// State manager (always delegate-backed)
	stateManager := state.NewManager(workspace, state.WithDelegate(memDelegate))

	// Session manager (always delegate-backed)
	sessionsDir := filepath.Join(workspace, "sessions")
	sessionsManager := session.NewSessionManager(sessionsDir, session.WithSessionDelegate(memDelegate, "dragonscale"))

	// One-shot DAG backfill for pre-existing session histories.
	backfillCtx, cancelBackfill := context.WithTimeout(ctx, 10*time.Second)
	defer cancelBackfill()
	if status, err := dag.BackfillMissingSessionDAGs(backfillCtx, memDelegate, queries, "dragonscale", dag.DefaultBackfillOptions()); err != nil {
		logger.WarnCF("agent", "DAG backfill failed (non-fatal)",
			map[string]interface{}{"error": err.Error()})
	} else if status != nil {
		logger.InfoCF("agent", "DAG backfill pass completed",
			map[string]interface{}{
				"sessions_scanned":  status.SessionsScanned,
				"snapshots_created": status.SnapshotsCreated,
				"skipped_existing":  status.SkippedExisting,
				"failures":          status.Failures,
				"skipped":           status.Skipped,
			})
	}

	// Meta-tools for progressive disclosure (tool_search + tool_call).
	// tool_search returns full parameter schemas; discovered tools are
	// dynamically promoted to native callables via PrepareStep.
	toolsRegistry.RegisterMetaTools()

	// Gateway tools: always visible to the LLM. Keep this set minimal —
	// dynamic promotion handles everything else after tool_search.
	for _, name := range []string{
		"memory",
		"read_file",
		"write_file",
		"list_dir",
		"exec",
	} {
		toolsRegistry.MarkGateway(name)
	}

	// Wire skills loader into tool_search for unified discovery
	if ts, ok := toolsRegistry.Get("tool_search"); ok {
		if tst, ok := ts.(*tools.ToolSearchTool); ok {
			tst.SetSkillsLoader(contextBuilder.SkillsLoader())
		}
	}

	// Observation manager
	callModelFn := func(ctx context.Context, prompt string) (string, error) {
		temp := 0.3
		maxTokens := int64(1024)
		resp, err := model.Generate(ctx, fantasy.Call{
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
	obsManager := observation.NewManager(memDelegate, "dragonscale", callModelFn, observation.DefaultManagerConfig())

	al := &AgentLoop{
		bus:              msgBus,
		languageModel:    model,
		workspace:        workspace,
		model:            cfg.Agents.Defaults.Model,
		contextWindow:    cfg.Agents.Defaults.MaxTokens,
		maxIterations:    cfg.Agents.Defaults.MaxToolIterations,
		sessions:         sessionsManager,
		state:            stateManager,
		contextBuilder:   contextBuilder,
		tools:            toolsRegistry,
		memoryStore:      ms,
		memDelegate:      memDelegate,
		obsManager:       obsManager,
		queries:          queries,
		kvDelegate:       kv,
		stateStore:       stateStore,
		toolResultSearch: NewToolResultSearchTool(queries, kv),
		identitySync:     idSync,
		summarizing:      sync.Map{},
		cfg:              cfg,
	}

	// Focus tools (start_focus / complete_focus)
	sessionKeyFn := func() string {
		if v := al.activeSessionKey.Load(); v != nil {
			return v.(string)
		}
		return ""
	}
	toolsRegistry.Register(tools.NewStartFocusTool(memDelegate, sessionsManager, sessionKeyFn))
	toolsRegistry.Register(tools.NewCompleteFocusTool(memDelegate, sessionsManager, sessionKeyFn))

	// DAG tools: dag_expand, dag_describe, dag_grep (require delegate-backed session)
	dagDeps := tools.DAGToolDeps{
		Queries:   queries,
		Lister:    del,
		Delegate:  del,
		AgentID:   "dragonscale",
		SessionFn: sessionKeyFn,
	}
	toolsRegistry.Register(tools.NewDagExpandTool(dagDeps))
	toolsRegistry.Register(tools.NewDagDescribeTool(dagDeps))
	toolsRegistry.Register(tools.NewDagGrepTool(dagDeps))

	// Unified runtime invariant: SecureBus is always enabled for tool execution.
	secretStore, err := initSecretStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create secret store: %w", err)
	}
	al.SetupSecureBus(secretStore, securebus.DefaultBusConfig())
	subagentManager.SetRunLoop(MakeUnifiedRunLoopFunc(al))

	return al, nil
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
	if al.identitySync != nil {
		al.identitySync.Close()
	}
	if al.memoryStore != nil {
		if err := al.memoryStore.Sync(); err != nil {
			logger.WarnCF("agent", "Failed to sync memory before shutdown",
				map[string]interface{}{"error": err.Error()})
		}
		al.memoryStore.Close()
	}
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

// SetSecureBus attaches a SecureBus to the agent loop. All tool
// calls are routed through the bus for capability enforcement, secret injection,
// leak scanning, and audit logging. Call before the first message is processed.
func (al *AgentLoop) SetSecureBus(b *securebus.Bus) {
	al.secureBus = b
}

// SetupSecureBus creates a SecureBus wired to this loop's tool registry and
// attaches it so all subsequent tool calls are routed through it.
// ss may be nil — secret injection is then disabled but all other enforcement
// (policy, leak scanning, audit) remains active.
// The returned Bus must be closed on shutdown.
func (al *AgentLoop) SetupSecureBus(ss *security.SecretStore, cfg securebus.BusConfig) *securebus.Bus {
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		t, ok := al.tools.Get(name)
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(t), true
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return al.tools.Execute(ctx, name, args)
	}
	b := securebus.New(cfg, ss, capLookup, executor)
	al.secureBus = b
	return b
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(ctx context.Context, channel string) error {
	return al.state.SetLastChannel(ctx, channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(ctx context.Context, chatID string) error {
	return al.state.SetLastChatID(ctx, chatID)
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
		SessionKey:    msg.SessionKey,
		Channel:       msg.Channel,
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		UserMessage:   msg.Content,
		EnableSummary: true,
		SendResponse:  false,
		Streaming:     true,
	})
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
// It injects the active session's summary so the agent has awareness of
// recent user conversation context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	if v := al.activeSessionKey.Load(); v != nil {
		if key, ok := v.(string); ok && key != "" {
			if summary := al.sessions.GetSummary(key); summary != "" {
				content = content + "\n\n## Recent User Context\n" + summary
			}
		}
	}
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:    "heartbeat",
		Channel:       channel,
		ChatID:        chatID,
		UserMessage:   content,
		EnableSummary: false,
		SendResponse:  false,
		NoHistory:     true,
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
		SessionKey:    msg.SessionKey,
		Channel:       msg.Channel,
		ChatID:        msg.ChatID,
		UserMessage:   msg.Content,
		EnableSummary: true,
		SendResponse:  false,
	})
}

func (al *AgentLoop) processSystemMessage(_ context.Context, msg bus.InboundMessage) (string, error) {
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

// assembledContext holds the pre-processed context produced by assembleContext,
// consumed by both the Generate and Stream code paths.
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
	if opts.Channel != "" && opts.ChatID != "" {
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(ctx, channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	logger.DebugCF("agent", "assembleContext: starting",
		map[string]interface{}{
			"session_key": opts.SessionKey,
			"channel":     opts.Channel,
			"sender_id":   opts.SenderID,
		})
	al.updateToolContexts(opts.Channel, opts.ChatID)

	block := al.obsManager.LoadBlock(ctx, opts.SessionKey)
	al.contextBuilder.SetObservationBlock(block)

	kb := tools.LoadKnowledgeBlock(ctx, al.memDelegate, opts.SessionKey)
	al.contextBuilder.SetKnowledgeBlock(kb)

	var history []messages.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}

	history = al.applyDAGCompression(ctx, opts.SessionKey, history)

	if al.identitySync != nil {
		_ = al.identitySync.CheckAndSync(ctx)
	}

	builtMsgs := al.contextBuilder.BuildMessages(history, summary, opts.UserMessage, nil, opts.Channel, opts.ChatID)
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	systemPrompt := ""
	var historyMsgs []messages.Message
	userPrompt := opts.UserMessage

	if len(builtMsgs) > 0 && builtMsgs[0].Role == "system" {
		systemPrompt = builtMsgs[0].Content
		if len(builtMsgs) > 2 {
			historyMsgs = builtMsgs[1 : len(builtMsgs)-1]
		}
	}

	logger.DebugCF("agent", "assembleContext: history messages",
		map[string]interface{}{
			"history": formatMessagesForLog(historyMsgs),
		})
	fantasyHistory := picofantasy.MessagesToFantasy(historyMsgs)

	adaptCfg := picofantasy.AdaptedToolsConfig{
		MemStore:   al.memoryStore,
		AgentID:    "dragonscale",
		SessionKey: opts.SessionKey,
	}
	adaptedTools := picofantasy.BuildAdaptedTools(al.tools, al.bus, opts.Channel, opts.ChatID, adaptCfg)
	if al.toolResultSearch != nil {
		adaptedTools = append(adaptedTools, al.toolResultSearch)
	}

	// Dynamic tool promotion via PrepareStep: after tool_search discovers tools,
	// they become native callables in the next inference step — no tool_call needed.
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

		var newTools []tools.Tool
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

	conversationID, runID, err := al.prepareRuntimeState(ctx, opts.SessionKey)
	if err != nil {
		return assembledContext{}, err
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
	agent := fantasy.NewAgent(al.languageModel, agentOpts...)

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
			AgentID:    "dragonscale",
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

// maybeSummarize only triggers emergency compression when hard limits are exceeded.
// Normal background compaction is intentionally disabled for the unified kernel.
func (al *AgentLoop) maybeSummarize(ctx context.Context, sessionKey, channel, chatID string) {
	_ = channel
	_ = chatID
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	criticalThreshold := al.contextWindow * 95 / 100

	if tokenEstimate > criticalThreshold {
		al.forceCompression(ctx, sessionKey)
	}

	// TODO: actually use the channel and chatID to push data to the bus
	//if _, loading := al.summarizing.LoadOrStore(sessionKey, true); !loading {
	//		go func() {
	//			defer al.summarizing.Delete(sessionKey)
	//			if !constants.IsInternalChannel(channel) {
	//				al.bus.PublishOutbound(bus.OutboundMessage{
	//					Channel: channel,
	//					ChatID:  chatID,
	//					Content: "⚠️ Memory threshold reached. Optimizing conversation history...",
	//				})
	//			}
	//			al.summarizeSession(ctx, sessionKey)
	//		}()
	//	}
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
		AgentID:    "dragonscale",
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
func (al *AgentLoop) forceCompression(ctx context.Context, sessionKey string) {
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

// MemoryDelegate returns the active memory delegate (nil if memory system is disabled).
func (al *AgentLoop) MemoryDelegate() memory.MemoryDelegate {
	return al.memDelegate
}

// HasSecureBus reports whether SecureBus enforcement is active.
func (al *AgentLoop) HasSecureBus() bool {
	return al.secureBus != nil
}

// HasUnifiedRuntimeDeps reports whether unified runtime persistence dependencies
// are available.
func (al *AgentLoop) HasUnifiedRuntimeDeps() bool {
	return al.queries != nil && al.kvDelegate != nil && al.stateStore != nil
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
		if err := al.memDelegate.UpsertKV(ctx, "dragonscale", tools.DAGRecoveryKVKey(sessionKey, nodeID), string(data)); err != nil {
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
			logger.WarnCF("agent", "Failed to persist DAG recovery references for oversized messages",
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

// applyDAGCompression compresses old history into a DAG summary block and
// returns only the tail messages that should be passed as raw conversation.
// The compressed portion is injected into the system prompt via contextBuilder.
// When memDelegate implements dag.DAGPersister, the DAG is persisted for dag_expand/describe/grep.
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

	dagMsgs := make([]dag.Message, len(compressible))
	for i, m := range compressible {
		dagMsgs[i] = dag.Message{Role: m.Role, Content: m.Content}
	}

	compressor := dag.NewCompressor(dag.DefaultCompressorConfig())
	d := compressor.Compress(dagMsgs)

	rendered := dag.RenderDAGForBudget(d, budget.DAGSummaries)
	al.contextBuilder.SetDAGBlock(rendered)

	// Persist DAG for dag_expand, dag_describe, dag_grep (additive; in-memory behavior unchanged)
	if dp, ok := al.memDelegate.(dag.DAGPersister); ok {
		if err := dp.PersistDAG(ctx, "dragonscale", sessionKey, &dag.PersistSnapshot{
			FromMsgIdx: 0,
			ToMsgIdx:   len(compressible),
			MsgCount:   len(compressible),
			DAG:        d,
		}); err != nil {
			logger.WarnCF("agent", "DAG persist failed (non-fatal)",
				map[string]interface{}{"error": err.Error(), "session_key": sessionKey})
		}
	}

	logger.DebugCF("agent", "DAG compression applied",
		map[string]interface{}{
			"total_msgs":      len(history),
			"compressed_msgs": len(compressible),
			"tail_msgs":       len(tail),
			"dag_nodes":       len(d.Nodes),
		})

	return tail
}

// estimateTokens estimates the number of tokens in a message list.
// listConfiguredModels returns a human-readable summary of which providers
// have API credentials configured, and the current default model.
func listConfiguredModels(cfg *config.Config) string {
	if cfg == nil {
		return "No configuration available."
	}

	current := fmt.Sprintf("Current model: %s", cfg.Agents.Defaults.Model)
	if cfg.Agents.Defaults.Provider != "" {
		current += fmt.Sprintf(" (provider: %s)", cfg.Agents.Defaults.Provider)
	}

	configured := cfg.Providers.ConfiguredNames()
	if len(configured) == 0 {
		return current + "\nNo providers configured — set API keys in config.json or environment variables."
	}

	return current + "\nConfigured providers: " + strings.Join(configured, ", ")
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

// FIXME: Leverage Cobra with subcommand command palette pattern for commands
func (al *AgentLoop) handleCommand(_ context.Context, msg bus.InboundMessage) (string, bool) {
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
			return listConfiguredModels(al.cfg), true
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
			// FIXME: This changes the 'default' channel for some operations, or effectively redirects output?
			// For now, let's just validate if the channel exists
			if al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			if _, exists := al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Sprintf("Channel '%s' not found or not enabled", value), true
			}

			// FIXME: If message came from CLI, maybe we want to redirect CLI output to this channel?
			// That would require state persistence about "redirected channel"
			// For now, just acknowledged.
			return fmt.Sprintf("Switched target channel to %s (Note: this currently only validates existence)", value), true
		default:
			return fmt.Sprintf("Unknown switch target: %s", target), true
		}
	}

	return "", false
}

func toolNames(tt []tools.Tool) []string {
	names := make([]string, len(tt))
	for i, t := range tt {
		names[i] = t.Name()
	}
	return names
}
